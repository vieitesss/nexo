package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/shell"
)

// SetupPromptDefaults seeds the guided setup form. Explicit scope selections
// are marked before the form opens and are never replaced by discovery.
type SetupPromptDefaults struct {
	Backend        string
	Notify         bool
	TUIKey         string
	AgentsKey      string
	Hosts          []string
	SelectedHosts  []string
	LocalOnly      bool
	ScopeLocked    bool
	Paths          []string
	RemoteBackends map[string]string
}

// SetupPromptValues is the answer returned by PromptSetup.
type SetupPromptValues struct {
	Backend        string
	Notify         bool
	TUIKey         string
	AgentsKey      string
	Hosts          []string
	Paths          []string
	RemoteBackends map[string]string
}

// ErrSetupCanceled distinguishes an intentional Escape from a form failure.
// Callers can surface it as the normal "setup canceled" decline path.
var ErrSetupCanceled = errors.New("setup canceled")

type setupPromptDone struct{ values SetupPromptValues }
type setupPromptModel struct {
	form           *stagedForm
	value          *SetupPromptValues
	err            error
	defaultBackend string
}

// PromptSetup runs the shared Bubble Tea staged-form stack. It is never called
// for a non-terminal stdin.
func PromptSetup(ctx context.Context, input io.Reader, output io.Writer, defaults SetupPromptDefaults) (SetupPromptValues, error) {
	stages := []formStage{
		{ID: "backend", Title: "backend", Fields: []formField{{ID: "backend", Label: "backend", Default: defaults.Backend, Kind: formChoice, Options: []string{"tmux", "herdr"}}}},
		{ID: "paths", Title: "project roots", Fields: []formField{{ID: "paths", Label: "paths (comma-separated)", Default: abbreviatePathList(strings.Join(defaults.Paths, ", ")), Kind: formText, Placeholder: "~/src, ~/work", AbbreviateHome: true}}},
		{ID: "notify", Title: "agents", Fields: []formField{{ID: "notify", Label: "desktop notifications", Default: boolLabel(defaults.Notify), Kind: formToggle}}},
		{ID: "bindings", Title: bindingTitle(defaults.Backend), Fields: []formField{{ID: "tui-key", Label: "TUI key (blank keeps opt-in)", Default: defaults.TUIKey, Kind: formText, Placeholder: "C-s", Validate: setupBindingValidator(defaults.Backend)}, {ID: "agents-key", Label: "agents key (blank keeps opt-in)", Default: defaults.AgentsKey, Kind: formText, Placeholder: "a", Validate: setupBindingValidator(defaults.Backend)}}},
	}
	if !defaults.LocalOnly {
		selected := make([]string, 0, len(defaults.SelectedHosts))
		selectedSet := make(map[string]bool, len(defaults.SelectedHosts))
		for _, host := range defaults.SelectedHosts {
			selectedSet[strings.ToLower(strings.TrimSpace(host))] = true
		}
		for _, host := range defaults.Hosts {
			if selectedSet[strings.ToLower(strings.TrimSpace(host))] {
				selected = append(selected, host)
			}
		}
		hostFields := []formField{{ID: "hosts", Label: "SSH aliases", Kind: formMultiSelect, Options: append([]string(nil), defaults.Hosts...), InitialSelected: selected}}
		if !defaults.ScopeLocked {
			hostFields = append(hostFields, formField{ID: "extra-host", Label: "additional alias (optional)", Kind: formText, Default: "", Placeholder: "ssh alias"})
		}
		stages = append(stages, formStage{ID: "hosts", Title: "remote machines", Fields: hostFields})
	}
	remoteMachines := make([]string, 0, len(defaults.RemoteBackends))
	for machine := range defaults.RemoteBackends {
		remoteMachines = append(remoteMachines, machine)
	}
	sort.Strings(remoteMachines)
	for _, machine := range remoteMachines {
		stages = append(stages, formStage{ID: "remote-backend:" + machine, Title: "backend for " + machine, Fields: []formField{{ID: "remote-backend:" + machine, Label: "backend", Default: defaults.RemoteBackends[machine], Kind: formChoice, Options: []string{"tmux", "herdr"}}}})
	}
	form := newStagedForm(ctx, stages, func(f *stagedForm) formValidation {
		return validateSetupForm(f, defaults.Backend)
	}, func(_ context.Context, values formValues, _ uint64) tea.Cmd {
		return func() tea.Msg {
			answer := SetupPromptValues{Backend: strings.ToLower(strings.TrimSpace(values.String("backend"))), Notify: values.Bool("notify")}
			for _, raw := range strings.Split(values.String("paths"), ",") {
				if path := strings.TrimSpace(raw); path != "" {
					answer.Paths = append(answer.Paths, path)
				}
			}
			answer.TUIKey, answer.AgentsKey = strings.TrimSpace(values.String("tui-key")), strings.TrimSpace(values.String("agents-key"))
			answer.RemoteBackends = make(map[string]string, len(defaults.RemoteBackends))
			for _, machine := range remoteMachines {
				answer.RemoteBackends[machine] = strings.ToLower(strings.TrimSpace(values.String("remote-backend:" + machine)))
			}
			selectedHosts := make(map[string]bool)
			for _, host := range values.List("hosts") {
				selectedHosts[strings.ToLower(strings.TrimSpace(host))] = true
			}
			for _, host := range defaults.Hosts {
				if selectedHosts[strings.ToLower(strings.TrimSpace(host))] {
					answer.Hosts = append(answer.Hosts, host)
				}
			}
			for _, raw := range strings.Fields(values.String("extra-host")) {
				if raw = strings.TrimSpace(raw); raw != "" {
					answer.Hosts = appendUnique(answer.Hosts, raw)
				}
			}
			return setupPromptDone{values: answer}
		}
	})
	model := setupPromptModel{form: form, defaultBackend: defaults.Backend}
	model.refreshBindingPresentation()
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output))
	result, err := program.Run()
	if err != nil {
		return SetupPromptValues{}, fmt.Errorf("run setup form: %w", err)
	}
	final, ok := result.(setupPromptModel)
	if !ok || final.value == nil {
		if final.err != nil {
			return SetupPromptValues{}, final.err
		}
		return SetupPromptValues{}, fmt.Errorf("setup form returned no answer")
	}
	return *final.value, nil
}

// PromptRemoteBackends asks only the questions whose machines were found
// ambiguous after the host-selection stage. Keeping this as a second form
// means aliases typed into that stage are included in the final question list.
func PromptRemoteBackends(ctx context.Context, input io.Reader, output io.Writer, backends map[string]string) (map[string]string, error) {
	machines := make([]string, 0, len(backends))
	for machine := range backends {
		machines = append(machines, machine)
	}
	sort.Strings(machines)
	stages := make([]formStage, 0, len(machines))
	for _, machine := range machines {
		stages = append(stages, formStage{ID: "remote-backend:" + machine, Title: "backend for " + machine, Fields: []formField{{ID: "remote-backend:" + machine, Label: "backend", Default: backends[machine], Kind: formChoice, Options: []string{"tmux", "herdr"}}}})
	}
	form := newStagedForm(ctx, stages, func(f *stagedForm) formValidation {
		return validateSetupForm(f, "")
	}, func(_ context.Context, values formValues, _ uint64) tea.Cmd {
		return func() tea.Msg {
			answer := SetupPromptValues{RemoteBackends: make(map[string]string, len(machines))}
			for _, machine := range machines {
				answer.RemoteBackends[machine] = strings.ToLower(strings.TrimSpace(values.String("remote-backend:" + machine)))
			}
			return setupPromptDone{values: answer}
		}
	})
	model := setupPromptModel{form: form}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output))
	result, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("run remote backend form: %w", err)
	}
	final, ok := result.(setupPromptModel)
	if !ok || final.value == nil {
		if final.err != nil {
			return nil, final.err
		}
		return nil, fmt.Errorf("remote backend form returned no answer")
	}
	return final.value.RemoteBackends, nil
}

func setupBindingValidator(defaultBackend string) func(string, formValues) error {
	return func(value string, values formValues) error {
		backend := strings.TrimSpace(values.String("backend"))
		if backend == "" {
			backend = defaultBackend
		}
		return config.ValidateBindingKeyForBackend(backend, value)
	}
}

func validateSetupForm(f *stagedForm, defaultBackend string) formValidation {
	values := f.values()
	backend := strings.ToLower(strings.TrimSpace(values.String("backend")))
	if backend == "" {
		backend = strings.ToLower(strings.TrimSpace(defaultBackend))
	}
	var result formValidation
	for stageIndex, stage := range f.stages {
		if stageIndex > f.stage {
			break
		}
		for index := range stage.Fields {
			field := &stage.Fields[index]
			switch {
			case field.ID == "backend":
				if backend != "tmux" && backend != "herdr" {
					field.setError("choose tmux or herdr")
					if result.Error == "" {
						result.Error = "backend must be tmux or herdr"
					}
				} else {
					field.setError("")
				}
			case field.ID == "tui-key" || field.ID == "agents-key":
				// A backend change does not edit the input, so revalidate here and
				// let setError keep the textinput in lockstep with the field.
				err := config.ValidateBindingKeyForBackend(backend, values.String(field.ID))
				if err != nil {
					field.setError(err.Error())
					if result.Error == "" {
						result.Error = "choose a valid binding key or leave it blank"
					}
				} else {
					field.setError("")
				}
			case strings.HasPrefix(field.ID, "remote-backend:"):
				remoteBackend := strings.ToLower(strings.TrimSpace(values.String(field.ID)))
				if remoteBackend != "tmux" && remoteBackend != "herdr" {
					field.setError("choose tmux or herdr")
					if result.Error == "" {
						result.Error = "each remote backend must be tmux or herdr"
					}
				} else {
					field.setError("")
				}
			}
		}
	}
	return result
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if strings.EqualFold(current, value) {
			return values
		}
	}
	return append(values, value)
}

func bindingTitle(backend string) string {
	if strings.EqualFold(strings.TrimSpace(backend), "tmux") {
		return "optional bindings (suggested tmux keys: C-s, a)"
	}
	return "optional bindings (suggested Herdr keys: ctrl+s, prefix+a)"
}

func bindingPlaceholders(backend string) (tuiKey, agentsKey string) {
	if strings.EqualFold(strings.TrimSpace(backend), "tmux") {
		return "C-s", "a"
	}
	return "ctrl+s", "prefix+a"
}

func (m setupPromptModel) refreshBindingPresentation() {
	if m.form == nil {
		return
	}
	backend := strings.ToLower(strings.TrimSpace(m.form.values().String("backend")))
	if backend == "" {
		backend = strings.ToLower(strings.TrimSpace(m.defaultBackend))
	}
	tuiPlaceholder, agentsPlaceholder := bindingPlaceholders(backend)
	for stageIndex := range m.form.stages {
		stage := &m.form.stages[stageIndex]
		if stage.ID != "bindings" {
			continue
		}
		stage.Title = bindingTitle(backend)
		for fieldIndex := range stage.Fields {
			field := &stage.Fields[fieldIndex]
			switch field.ID {
			case "tui-key":
				field.Placeholder = tuiPlaceholder
				field.input.Placeholder = tuiPlaceholder
			case "agents-key":
				field.Placeholder = agentsPlaceholder
				field.input.Placeholder = agentsPlaceholder
			}
		}
	}
}

func (m setupPromptModel) Init() tea.Cmd {
	m.refreshBindingPresentation()
	if m.form == nil {
		return nil
	}
	return m.form.initialFocusCmd()
}
func (m setupPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		if m.form != nil {
			m.form.setViewportWidth(max(1, typed.Width))
			m.form.viewportHeight = max(1, typed.Height)
		}
		return m, nil
	case tea.KeyMsg:
		effect := m.form.key(typed)
		m.refreshBindingPresentation()
		if effect.Canceled {
			m.err = ErrSetupCanceled
			return m, tea.Quit
		}
		return m, effect.Cmd
	case stagedFormMsg:
		if m.form == nil || typed.generation != m.form.generation {
			return m, nil
		}
		effect := m.form.updateMsg(typed)
		return m, effect.Cmd
	case setupPromptDone:
		m.value = &typed.values
		return m, tea.Quit
	}
	return m, nil
}
func (m setupPromptModel) View() string {
	if m.form == nil {
		return ""
	}
	m.refreshBindingPresentation()
	lines := m.form.renderLines()
	if m.form.viewportWidth > 0 {
		lines = formLinesForWidth(lines, m.form.viewportWidth)
	}
	lines = setupViewportLines(lines, m.form.viewportHeight)
	return strings.Join(lines, "\n") + "\n"
}

func setupViewportLines(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	// The help footer is always the last line. Scroll only the body so it
	// remains visible even when the SSH alias checklist is long.
	footer := lines[len(lines)-1]
	body := lines[:len(lines)-1]
	bodyHeight := max(0, height-1)
	if bodyHeight == 0 {
		return []string{footer}
	}
	active := activeFormLine(body)
	start := 0
	if len(body) > bodyHeight {
		start = shell.ViewportStart(len(body), bodyHeight, active)
		body = body[start : start+bodyHeight]
	}
	return append(body, footer)
}

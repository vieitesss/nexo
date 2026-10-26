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
)

// SetupPromptDefaults seeds the guided setup form. Explicit scope selections
// are marked before the form opens and are never replaced by discovery.
type SetupPromptDefaults struct {
	Backend string
	// BackendDetected says how setup already knows Backend. When set, the
	// backend stage is shown as answered instead of asked.
	BackendDetected string
	Notify          bool
	// Bindings holds the keys already bound for each backend, so switching
	// the backend in the form offers that backend's keys instead of the
	// other one's.
	Bindings       map[string]SetupBindings
	Hosts          []string
	SelectedHosts  []string
	LocalOnly      bool
	ScopeLocked    bool
	Paths          []string
	RemoteBackends map[string]string
	// Theme is the [tui] theme: auto, light or dark. Setup's forms use the TUI
	// palette, so auto probes the terminal background the same way the TUI
	// does. Empty leaves lipgloss untouched.
	Theme string
}

// SetupBindings is one backend's TUI and agents keys.
type SetupBindings struct {
	TUIKey    string
	AgentsKey string
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
	bindings       *shownBindings
}

// shownBindings remembers which backend's keys the binding fields hold. It is
// a pointer because Bubble Tea copies the model on every update.
type shownBindings struct {
	backend   string
	byBackend map[string]SetupBindings
}

// PromptSetup runs the shared Bubble Tea staged-form stack. It is never called
// for a non-terminal stdin.
func PromptSetup(ctx context.Context, input io.Reader, output io.Writer, defaults SetupPromptDefaults) (SetupPromptValues, error) {
	tuiPlaceholder, agentsPlaceholder := bindingPlaceholders(defaults.Backend)
	initialBindings := defaults.Bindings[strings.ToLower(strings.TrimSpace(defaults.Backend))]
	stages := []formStage{
		{ID: "backend", Title: "Backend", Prompt: "Both tmux and Herdr are installed. Which one should setup connect nexo to?", Detected: defaults.BackendDetected, Fields: []formField{{ID: "backend", Default: defaults.Backend, Kind: formChoice, Options: config.SetupBackends}}},
		{ID: "paths", Title: "Project roots", Prompt: "Which folders hold your projects? Separate them with commas.", Examples: []formExample{
			{Input: "~/src", Meaning: "every folder in it is a project: ~/src/nexo, ~/src/dotfiles"},
			{Input: "~/work/*", Meaning: "one level deeper: ~/work/acme/api, ~/work/acme/web"},
		}, Fields: []formField{{ID: "paths", Default: abbreviatePathList(strings.Join(defaults.Paths, ", ")), Kind: formText, Placeholder: "~/src, ~/work", AbbreviateHome: true}}},
		{ID: "notify", Title: "Agents", Prompt: "Send a desktop notification when an agent stops?", Fields: []formField{{ID: "notify", Default: boolLabel(defaults.Notify), Kind: formToggle}}},
		{ID: "bindings", Title: "Key bindings", Prompt: bindingPrompt(defaults.Backend), Examples: bindingExamples(defaults.Backend), Fields: []formField{{ID: "tui-key", Label: "TUI key", Default: initialBindings.TUIKey, Kind: formText, Placeholder: tuiPlaceholder, Validate: setupBindingValidator(defaults.Backend)}, {ID: "agents-key", Label: "agents key", Default: initialBindings.AgentsKey, Kind: formText, Placeholder: agentsPlaceholder, Validate: setupBindingValidator(defaults.Backend)}}},
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
		hostFields := []formField{{ID: "hosts", Kind: formMultiSelect, Options: append([]string(nil), defaults.Hosts...), InitialSelected: selected}}
		if !defaults.ScopeLocked {
			hostFields = append(hostFields, formField{ID: "extra-host", Label: "another alias", Kind: formText, Default: "", Placeholder: "ssh alias"})
		}
		stages = append(stages, formStage{ID: "hosts", Title: "Remote machines", Prompt: "Which SSH machines should nexo list containers from? Space selects.", Fields: hostFields})
	}
	remoteMachines := make([]string, 0, len(defaults.RemoteBackends))
	for machine := range defaults.RemoteBackends {
		remoteMachines = append(remoteMachines, machine)
	}
	sort.Strings(remoteMachines)
	for _, machine := range remoteMachines {
		stages = append(stages, remoteBackendStage(machine, defaults.RemoteBackends[machine]))
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
	form.title = "nexo setup"
	if defaults.Theme != "" {
		// Probe before Bubble Tea owns the terminal. The remote backend form
		// runs later in the same process and keeps this choice.
		configureTheme(defaults.Theme)
	}
	model := setupPromptModel{form: form, defaultBackend: defaults.Backend, bindings: &shownBindings{backend: strings.ToLower(strings.TrimSpace(defaults.Backend)), byBackend: defaults.Bindings}}
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
		stages = append(stages, remoteBackendStage(machine, backends[machine]))
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
	form.title = "Remote backends"
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

func remoteBackendStage(machine, backend string) formStage {
	// A choice always starts on an option, so Enter alone answers it.
	if backend = strings.ToLower(strings.TrimSpace(backend)); backend != "herdr" {
		backend = "tmux"
	}
	return formStage{
		ID:     "remote-backend:" + machine,
		Title:  "Backend for " + machine,
		Prompt: "Both tmux and Herdr run on " + machine + ". Which one should nexo list?",
		Fields: []formField{{ID: "remote-backend:" + machine, Default: backend, Kind: formChoice, Options: []string{"tmux", "herdr"}}},
	}
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
			// A choice has nothing to edit, so its problem is reported once as
			// the stage error rather than under the control as well.
			case field.ID == "backend":
				if !config.IsSetupBackend(backend) && result.Error == "" {
					result.Error = "choose " + strings.Join(config.SetupBackends, ", ")
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
				if (remoteBackend != "tmux" && remoteBackend != "herdr") && result.Error == "" {
					result.Error = "choose tmux or herdr for " + strings.TrimPrefix(field.ID, "remote-backend:")
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

func bindingPrompt(backend string) string {
	if strings.EqualFold(strings.TrimSpace(backend), "rex") {
		return "Optional. A Rex binding is a key sequence such as ctrl+space>o. Leave a key blank to bind it yourself."
	}
	return "Optional. Either key can be pressed directly or after the prefix. Leave a key blank to bind it yourself."
}

// bindingExamples spell both kinds of binding the way the backend names keys.
// prefix+ means the same thing for tmux and Herdr.
func bindingExamples(backend string) []formExample {
	if strings.EqualFold(strings.TrimSpace(backend), "rex") {
		return []formExample{
			{Input: "ctrl+space>o", Meaning: "Ctrl-Space, then o"},
			{Input: "ctrl+alt+g", Meaning: "Ctrl-Alt-g, no sequence"},
		}
	}
	if strings.EqualFold(strings.TrimSpace(backend), "tmux") {
		return []formExample{
			{Input: "C-s", Meaning: "Ctrl-s, no prefix (bare keys need C- or M-)"},
			{Input: "prefix+a", Meaning: "the tmux prefix, then a"},
		}
	}
	return []formExample{
		{Input: "ctrl+s", Meaning: "Ctrl-s, no prefix"},
		{Input: "prefix+a", Meaning: "the Herdr prefix, then a"},
	}
}

func bindingPlaceholders(backend string) (tuiKey, agentsKey string) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "tmux":
		return "C-s", "prefix+a"
	case "rex":
		return "ctrl+space>o", "ctrl+space>a"
	default:
		return "ctrl+s", "prefix+a"
	}
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
	var previous, next SetupBindings
	switched := m.bindings != nil && m.bindings.backend != backend
	if switched {
		previous, next = m.bindings.byBackend[m.bindings.backend], m.bindings.byBackend[backend]
		m.bindings.backend = backend
	}
	values := m.form.values()
	for stageIndex := range m.form.stages {
		stage := &m.form.stages[stageIndex]
		if stage.ID != "bindings" {
			continue
		}
		stage.Examples = bindingExamples(backend)
		for fieldIndex := range stage.Fields {
			field := &stage.Fields[fieldIndex]
			switch field.ID {
			case "tui-key":
				field.Placeholder = tuiPlaceholder
				field.input.Placeholder = tuiPlaceholder
				// A key the user typed stays; one that only came from the
				// previous backend's config follows the backend.
				if switched && values.String(field.ID) == previous.TUIKey {
					field.setValue(next.TUIKey)
				}
			case "agents-key":
				field.Placeholder = agentsPlaceholder
				field.input.Placeholder = agentsPlaceholder
				if switched && values.String(field.ID) == previous.AgentsKey {
					field.setValue(next.AgentsKey)
				}
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
	if m.err != nil {
		// A canceled form leaves nothing behind; the caller reports it.
		return ""
	}
	m.refreshBindingPresentation()
	lines := m.form.renderLines()
	if m.value != nil {
		// The last frame stays in the scrollback, so leave the answers there
		// instead of a busy line.
		lines = append(m.form.summaryLines(), "")
	}
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
	body = viewportFormLines(body, bodyHeight, 0, false)
	return append(body, footer)
}

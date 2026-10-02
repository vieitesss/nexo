package tui

import (
	"context"
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

type setupPromptDone struct{ values SetupPromptValues }
type setupPromptModel struct {
	form  *stagedForm
	value *SetupPromptValues
	err   error
}

// PromptSetup runs the shared Bubble Tea staged-form stack. It is never called
// for a non-terminal stdin.
func PromptSetup(ctx context.Context, input io.Reader, output io.Writer, defaults SetupPromptDefaults) (SetupPromptValues, error) {
	stages := []formStage{
		{ID: "backend", Title: "backend", Fields: []formField{{ID: "backend", Label: "backend (tmux or herdr)", Value: defaults.Backend}}},
		{ID: "paths", Title: "project roots (comma-separated)", Fields: []formField{{ID: "paths", Label: "paths", Value: strings.Join(defaults.Paths, ", ")}}},
		{ID: "notify", Title: "agents", Fields: []formField{{ID: "notify", Label: "desktop notifications", Value: boolLabel(defaults.Notify)}}},
		{ID: "bindings", Title: bindingTitle(defaults.Backend), Fields: []formField{{ID: "tui-key", Label: "TUI key (blank keeps opt-in)", Value: defaults.TUIKey}, {ID: "agents-key", Label: "agents key (blank keeps opt-in)", Value: defaults.AgentsKey}}},
	}
	if !defaults.LocalOnly {
		hostFields := make([]formField, 0, len(defaults.Hosts)+1)
		selected := make(map[string]bool, len(defaults.SelectedHosts))
		for _, host := range defaults.SelectedHosts {
			selected[strings.ToLower(strings.TrimSpace(host))] = true
		}
		for _, host := range defaults.Hosts {
			if selected[strings.ToLower(strings.TrimSpace(host))] {
				hostFields = append(hostFields, formField{ID: "host:" + host, Label: host, Value: "✓", ReadOnly: true})
			} else {
				hostFields = append(hostFields, formField{ID: "host:" + host, Label: host, Value: "", ReadOnly: true})
			}
		}
		if !defaults.ScopeLocked {
			hostFields = append(hostFields, formField{ID: "extra-host", Label: "additional alias (optional)", Value: ""})
		}
		stages = append(stages, formStage{ID: "hosts", Title: "remote machines (Space toggles)", Fields: hostFields})
	}
	remoteMachines := make([]string, 0, len(defaults.RemoteBackends))
	for machine := range defaults.RemoteBackends {
		remoteMachines = append(remoteMachines, machine)
	}
	sort.Strings(remoteMachines)
	for _, machine := range remoteMachines {
		stages = append(stages, formStage{ID: "remote-backend:" + machine, Title: "backend for " + machine, Fields: []formField{{ID: "remote-backend:" + machine, Label: "tmux or herdr", Value: defaults.RemoteBackends[machine]}}})
	}
	form := newStagedForm(ctx, stages, func(f *stagedForm) formValidation {
		for stageIndex, stage := range f.stages {
			if stageIndex > f.stage {
				break
			}
			for index := range stage.Fields {
				field := &stage.Fields[index]
				if field.ID == "backend" {
					backend := strings.ToLower(strings.TrimSpace(field.Value))
					if backend != "tmux" && backend != "herdr" {
						field.Error = "choose tmux or herdr"
						return formValidation{Error: "backend must be tmux or herdr"}
					}
				}
				if field.ID == "tui-key" || field.ID == "agents-key" {
					if err := config.ValidateBindingKey(field.Value); err != nil {
						field.Error = err.Error()
						return formValidation{Error: "choose a valid binding key or leave it blank"}
					}
				}
				if strings.HasPrefix(field.ID, "remote-backend:") {
					backend := strings.ToLower(strings.TrimSpace(field.Value))
					if backend != "tmux" && backend != "herdr" {
						field.Error = "choose tmux or herdr"
						return formValidation{Error: "each remote backend must be tmux or herdr"}
					}
				}
			}
		}
		return formValidation{}
	}, func(_ context.Context, values map[string]string, _ uint64) tea.Cmd {
		return func() tea.Msg {
			answer := SetupPromptValues{Backend: strings.ToLower(strings.TrimSpace(values["backend"])), Notify: values["notify"] == "yes"}
			for _, raw := range strings.Split(values["paths"], ",") {
				if path := strings.TrimSpace(raw); path != "" {
					answer.Paths = append(answer.Paths, path)
				}
			}
			answer.TUIKey, answer.AgentsKey = strings.TrimSpace(values["tui-key"]), strings.TrimSpace(values["agents-key"])
			answer.RemoteBackends = make(map[string]string, len(defaults.RemoteBackends))
			for _, machine := range remoteMachines {
				answer.RemoteBackends[machine] = strings.ToLower(strings.TrimSpace(values["remote-backend:"+machine]))
			}
			for _, host := range defaults.Hosts {
				if values["host:"+host] == "✓" {
					answer.Hosts = append(answer.Hosts, host)
				}
			}
			for _, raw := range strings.Fields(values["extra-host"]) {
				if raw = strings.TrimSpace(raw); raw != "" {
					answer.Hosts = appendUnique(answer.Hosts, raw)
				}
			}
			return setupPromptDone{values: answer}
		}
	})
	form.keyHook = func(msg tea.KeyMsg) (formEffect, bool) {
		field := form.currentField()
		if field == nil || msg.Type != tea.KeySpace {
			return formEffect{}, false
		}
		switch {
		case strings.HasPrefix(field.ID, "host:"):
			if field.Value == "✓" {
				field.Value = ""
			} else {
				field.Value = "✓"
			}
			return formEffect{Changed: true}, true
		case field.ID == "notify":
			if field.Value == "yes" {
				field.Value = "no"
			} else {
				field.Value = "yes"
			}
			return formEffect{Changed: true}, true
		default:
			return formEffect{}, false
		}
	}
	model := setupPromptModel{form: form}
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

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func (m setupPromptModel) Init() tea.Cmd { return nil }
func (m setupPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyMsg:
		effect := m.form.key(typed)
		if effect.Canceled {
			return m, tea.Quit
		}
		if effect.Submitted {
			return m, effect.Cmd
		}
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
	return strings.Join(m.form.renderLines(), "\n") + "\n"
}

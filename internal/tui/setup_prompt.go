package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SetupPromptDefaults seeds the guided setup form. Host fields are real
// multi-select rows: Space toggles the highlighted alias and the submitted
// value contains only selected aliases.
type SetupPromptDefaults struct {
	Backend   string
	Notify    bool
	TUIKey    string
	AgentsKey string
	Hosts     []string
}

// SetupPromptValues is the answer returned by PromptSetup.
type SetupPromptValues struct {
	Backend   string
	Notify    bool
	TUIKey    string
	AgentsKey string
	Hosts     []string
}

type setupPromptDone struct{ values SetupPromptValues }
type setupPromptModel struct {
	form  *stagedForm
	value *SetupPromptValues
	err   error
}

// PromptSetup runs the same Bubble Tea staged-form stack used by the other
// commands. It is intentionally not called by setup when stdin is not a TTY.
func PromptSetup(ctx context.Context, input io.Reader, output io.Writer, defaults SetupPromptDefaults) (SetupPromptValues, error) {
	stages := []formStage{
		{ID: "backend", Title: "backend", Fields: []formField{{ID: "backend", Label: "backend (tmux or herdr)", Value: defaults.Backend}}},
		{ID: "notify", Title: "agents", Fields: []formField{{ID: "notify", Label: "desktop notifications", Value: boolLabel(defaults.Notify)}}},
		{ID: "bindings", Title: "optional bindings (leave blank to keep opt-in)", Fields: []formField{{ID: "tui-key", Label: "TUI key", Value: defaults.TUIKey}, {ID: "agents-key", Label: "agents key", Value: defaults.AgentsKey}}},
	}
	hostFields := make([]formField, 0, len(defaults.Hosts))
	for _, host := range defaults.Hosts {
		hostFields = append(hostFields, formField{ID: "host:" + host, Label: host, Value: "✓", ReadOnly: true})
	}
	if len(hostFields) > 0 {
		stages = append(stages, formStage{ID: "hosts", Title: "remote machines (Space toggles)", Fields: hostFields})
	}
	form := newStagedForm(ctx, stages, func(*stagedForm) formValidation { return formValidation{} }, func(_ context.Context, values map[string]string, _ uint64) tea.Cmd {
		return func() tea.Msg {
			answer := SetupPromptValues{Backend: strings.ToLower(strings.TrimSpace(values["backend"])), Notify: values["notify"] == "yes", TUIKey: strings.TrimSpace(values["tui-key"]), AgentsKey: strings.TrimSpace(values["agents-key"])}
			for _, host := range defaults.Hosts {
				if values["host:"+host] == "✓" {
					answer.Hosts = append(answer.Hosts, host)
				}
			}
			return setupPromptDone{values: answer}
		}
	})
	form.keyHook = func(msg tea.KeyMsg) (formEffect, bool) {
		field := form.currentField()
		if field == nil {
			return formEffect{}, false
		}
		if msg.Type != tea.KeySpace {
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

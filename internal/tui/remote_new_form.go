package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// newRemoteNewForm reuses the generic staged form for the remote
// new-container name. It is only opened when no search query proposes one, so
// the field starts empty as an inline prompt instead of a second surface.
func newRemoteNewForm(ctx context.Context, target RemoteTarget) *stagedForm {
	form := newStagedForm(ctx, []formStage{{
		ID:     "name",
		Title:  "new container on " + target.Machine,
		Fields: []formField{{ID: "name", Label: "name"}},
	}}, remoteNewFormValidation(), remoteNewFormSubmit(target))
	form.busyLabel = "creating…"
	return form
}

func remoteNewFormValidation() func(*stagedForm) formValidation {
	return func(form *stagedForm) formValidation {
		name := strings.TrimSpace(formValue(form, "name"))
		if name == "" {
			return formValidation{Error: "name is required"}
		}
		if strings.ContainsAny(name, "\r\n") {
			return formValidation{Error: "name must be one line"}
		}
		return formValidation{Values: map[string]string{"name": name}}
	}
}

// remoteNewFormSubmit turns a validated name into a message. The coordinator
// closes the TUI and runs the remote New flow after it quits, so the focus
// policy never fights the still-painted interface.
func remoteNewFormSubmit(target RemoteTarget) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(_ context.Context, values map[string]string, _ uint64) tea.Cmd {
		name := strings.TrimSpace(values["name"])
		return func() tea.Msg { return remoteNewMsg{target: target, name: name} }
	}
}

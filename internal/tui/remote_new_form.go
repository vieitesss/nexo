package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// newRemoteNewForm reuses the generic staged form for the remote
// new-container name. It is only opened when no search query proposes one, so
// the field starts empty as an inline prompt instead of a second surface. An
// invalid query-derived name is prefilled so the shared policy reports it
// inline instead of quitting with a name the backend would reject.
func newRemoteNewForm(ctx context.Context, target RemoteTarget, initialName string) *stagedForm {
	form := newStagedForm(ctx, []formStage{{
		ID:     "name",
		Title:  "new container on " + target.Machine,
		Fields: []formField{{ID: "name", Label: "name", Value: initialName}},
	}}, remoteNewFormValidation(), remoteNewFormSubmit(target))
	form.busyLabel = "creating…"
	return form
}

// remoteContainerNameError is the one-line name policy shared by the query
// fast path and the staged form, so both surfaces reject the same input.
func remoteContainerNameError(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "name is required"
	}
	if strings.ContainsAny(name, "\r\n") {
		return "name must be one line"
	}
	return ""
}

func remoteNewFormValidation() func(*stagedForm) formValidation {
	return func(form *stagedForm) formValidation {
		name := strings.TrimSpace(formValue(form, "name"))
		if message := remoteContainerNameError(name); message != "" {
			return formValidation{Error: message}
		}
		return formValidation{Values: map[string]string{"name": name}}
	}
}

// remoteNewFormSubmit turns a validated name into a message. The coordinator
// closes the TUI and runs the remote New flow after it quits, so the focus
// policy never fights the still-painted interface. The message carries the
// submitted generation and the command honors cancellation, so a late result
// from a canceled or replaced form can never run the action.
func remoteNewFormSubmit(target RemoteTarget) func(context.Context, map[string]string, uint64) tea.Cmd {
	return func(actionCtx context.Context, values map[string]string, generation uint64) tea.Cmd {
		name := strings.TrimSpace(values["name"])
		return func() tea.Msg {
			if actionCtx != nil && actionCtx.Err() != nil {
				return nil
			}
			return remoteNewMsg{target: target, name: name, generation: generation}
		}
	}
}

package tui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
)

// placeActionKind marks the actionMsg of a configured project action.
const placeActionKind = "place"

// projectAction returns the configured action bound to key.
func (d Deps) projectAction(key string) (config.ProjectAction, bool) {
	for _, action := range d.ProjectActions {
		if action.Key == key {
			return action, true
		}
	}
	return config.ProjectAction{}, false
}

func (d Deps) supportsBehavior(behavior config.ProjectBehavior) bool {
	return d.PlaceProject != nil && slices.Contains(d.ProjectBehaviors, behavior)
}

// placeableProject reports the project row a configured action applies to.
func placeableProject(current row) (picker.Place, bool) {
	if current.kind != itemRow || current.place.Kind != picker.ProjectKind || current.place.Path == "" {
		return picker.Place{}, false
	}
	return current.place, true
}

// runProjectAction starts a configured action on the selected project. Keys
// outside a project row do nothing; an unsupported behavior explains itself
// in a toast and keeps the TUI open.
func (s *inputState) runProjectAction(action config.ProjectAction, rows []row) inputEffect {
	if s.tab != ProjectsTab {
		return inputEffect{}
	}
	current, ok := s.currentRow(rows)
	if !ok {
		return inputEffect{}
	}
	place, ok := placeableProject(current)
	if !ok {
		return inputEffect{}
	}
	if !s.deps.supportsBehavior(action.Behavior) {
		backend := s.deps.BackendName
		if backend == "" {
			backend = "this backend"
		}
		text := fmt.Sprintf("%s is not supported by %s", action.Behavior, backend)
		return inputEffect{cmd: func() tea.Msg { return ToastMsg{Text: text} }}
	}
	behavior, run, ctx := action.Behavior, s.deps.PlaceProject, s.ctx
	s.actionInFlight = true
	return inputEffect{cmd: func() tea.Msg {
		return actionMsg{kind: placeActionKind, err: run(ctx, place, behavior)}
	}}
}

// projectActionHints returns footer hints for the supported configured
// actions. They follow the built-in hints, so a narrow footer drops them first.
func (d Deps) projectActionHints() []string {
	var hints []string
	for _, action := range d.ProjectActions {
		if d.supportsBehavior(action.Behavior) {
			hints = append(hints, hint(action.DisplayKey(), action.DisplayLabel()))
		}
	}
	return hints
}

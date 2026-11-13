package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

type placeCall struct {
	path     string
	behavior config.ProjectBehavior
}

// projectActionDeps has one project and two actions: split (supported) and
// float (not supported), as on a backend without floating panes.
func projectActionDeps(calls *[]placeCall, err error) Deps {
	return Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: "/src/api", Group: "/src", DisplayRemainder: "api"}},
		ProjectActions: []config.ProjectAction{
			{Key: "ctrl+s", Behavior: config.BehaviorSplitRight, Label: "split"},
			{Key: "alt+g", Behavior: config.BehaviorFloat},
		},
		ProjectBehaviors: []config.ProjectBehavior{config.BehaviorSplitRight},
		BackendName:      "herdr",
		PlaceProject: func(_ context.Context, place picker.Place, behavior config.ProjectBehavior) error {
			*calls = append(*calls, placeCall{place.Path, behavior})
			return err
		},
	}
}

func projectActionModel(t *testing.T, deps Deps) Model {
	t.Helper()
	model := NewModel(context.Background(), deps, ProjectsTab)
	model.SetSize(120, 10)
	// Folders start folded: unfold the header, then step onto the project.
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	model = updateModel(t, model, keyMsg(tea.KeyDown))
	if _, ok := model.frame().currentItem(); !ok {
		t.Fatal("fixture did not select the project row")
	}
	return model
}

func TestProjectActionPlacesProjectThenQuits(t *testing.T) {
	var calls []placeCall
	model := projectActionModel(t, projectActionDeps(&calls, nil))
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if cmd == nil || !model.input.actionInFlight {
		t.Fatal("action did not start while the TUI was open")
	}
	updated, cmd = model.Update(cmd())
	model = updated.(Model)
	if len(calls) != 1 || calls[0] != (placeCall{"/src/api", config.BehaviorSplitRight}) {
		t.Fatalf("PlaceProject calls = %v", calls)
	}
	if cmd == nil {
		t.Fatal("success did not close the TUI")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("success did not close the TUI")
	}
	if err := model.execute(context.Background()); err != nil {
		t.Fatalf("execute after a placed project = %v, want nothing else to run", err)
	}
}

func TestProjectActionFailureStaysOpenWithToast(t *testing.T) {
	var calls []placeCall
	model := projectActionModel(t, projectActionDeps(&calls, errors.New("rex split: no such block")))
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	updated, cmd = model.Update(cmd())
	model = updated.(Model)
	if cmd != nil || model.input.actionInFlight || model.toast != "rex split: no such block" {
		t.Fatalf("failure: quit=%v inFlight=%v toast=%q", cmd != nil, model.input.actionInFlight, model.toast)
	}
}

func TestUnsupportedProjectActionExplainsAndStaysOpen(t *testing.T) {
	var calls []placeCall
	model := projectActionModel(t, projectActionDeps(&calls, nil))
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}, Alt: true})
	model = updated.(Model)
	if cmd == nil || model.input.actionInFlight {
		t.Fatal("unsupported action did not answer with a toast")
	}
	model = updateModel(t, model, cmd())
	if len(calls) != 0 || model.toast != "float is not supported by herdr" {
		t.Fatalf("unsupported: calls=%v toast=%q", calls, model.toast)
	}
	if model.input.query.Value() != "" {
		t.Fatalf("action key leaked into search: %q", model.input.query.Value())
	}
}

func TestProjectActionIgnoredOffProjectRows(t *testing.T) {
	var calls []placeCall
	model := projectActionModel(t, projectActionDeps(&calls, nil))
	model = updateModel(t, model, keyMsg(tea.KeyUp)) // the folder header
	if _, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS}); cmd != nil {
		t.Fatal("action ran on a folder header")
	}
	model = NewModel(context.Background(), projectActionDeps(&calls, nil), ContainersTab)
	if _, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS}); cmd != nil {
		t.Fatal("action ran outside the Projects tab")
	}
}

func TestFooterShowsOnlySupportedProjectActions(t *testing.T) {
	var calls []placeCall
	model := projectActionModel(t, projectActionDeps(&calls, nil))
	footer := shell.Plain(model.frame().footer())
	if !strings.Contains(footer, "Ctrl-S split") {
		t.Fatalf("footer missing supported action: %q", footer)
	}
	if strings.Contains(footer, "Alt-G") {
		t.Fatalf("footer advertises unsupported action: %q", footer)
	}
	model = updateModel(t, model, keyMsg(tea.KeyUp))
	if footer := shell.Plain(model.frame().footer()); strings.Contains(footer, "Ctrl-S") {
		t.Fatalf("folder header footer advertises project action: %q", footer)
	}
}

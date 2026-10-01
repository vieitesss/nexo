package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
)

func TestCloneFormTransitionsAndSubmitsWithoutOpening(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "projects")
	if err := mkdirForTUITest(parent); err != nil {
		t.Fatal(err)
	}
	var gotSource, gotDestination string
	deps := Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "existing"), Group: root, DisplayRemainder: "existing"}},
		CloneProject: func(_ context.Context, source, destination string) error {
			gotSource, gotDestination = source, destination
			return nil
		},
		Home: root,
	}
	model := NewModel(context.Background(), deps, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlO})
	if model.input.form == nil {
		t.Fatal("Ctrl-O did not open the clone form")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("owner/repo")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.input.form.stage != 1 {
		t.Fatalf("form stage = %d, want destination stage", model.input.form.stage)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("~/projects/repo")})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || !model.input.form.inFlight {
		t.Fatal("destination Enter did not start asynchronous clone")
	}
	model = updateModel(t, model, cmd())
	if model.input.form != nil {
		t.Fatal("successful clone left the modal open")
	}
	if gotSource != "git@github.com:owner/repo" || gotDestination != filepath.Join(parent, "repo") {
		t.Fatalf("clone action = %q, %q", gotSource, gotDestination)
	}
}

func TestCloneFormKeepsInlineDestinationError(t *testing.T) {
	root := t.TempDir()
	deps := Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "existing"), Group: root}},
		Home:     root,
	}
	model := NewModel(context.Background(), deps, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("owner/repo")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(filepath.Join(root, "missing", "repo"))})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil || model.input.form == nil || model.input.form.stageError == "" {
		t.Fatalf("invalid destination transition = cmd %v, form %#v", cmd != nil, model.input.form)
	}
	model.SetSize(160, 20)
	if !strings.Contains(model.View(), "does not exist") {
		t.Fatalf("View() = %q, want inline destination error", model.View())
	}
}

func mkdirForTUITest(path string) error {
	return os.MkdirAll(path, 0o755)
}

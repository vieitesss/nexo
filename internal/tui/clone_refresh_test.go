package tui

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	cloneflow "github.com/vieitesss/nexo/internal/clone"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/startup"
)

func TestCloneSuccessRefreshesAndSelectsProject(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-repo")
	projects := []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "old"), Group: root, DisplayRemainder: "old"}}
	deps := Deps{
		Projects:     projects,
		StartupTrace: startup.New("test", io.Discard),
		ProjectSource: func(context.Context) ([]picker.Place, error) {
			return append(append([]picker.Place(nil), projects...), picker.Place{Kind: picker.ProjectKind, Path: destination, Group: root, DisplayRemainder: "new-repo"}), nil
		},
		CloneProject: func(context.Context, cloneflow.Request) error { return nil },
		Home:         root,
	}
	model := NewModel(context.Background(), deps, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("owner/repo")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(destination)})
	updated, action := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, reload := model.Update(action())
	model = updated.(Model)
	if reload == nil {
		t.Fatal("clone success did not request a project refresh")
	}
	updated, _ = model.Update(reload())
	model = updated.(Model)
	if model.Cursor() != "project:"+destination {
		t.Fatalf("cursor = %q, want refreshed project", model.Cursor())
	}
}

package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/startup"
)

func openDirectoryForm(t *testing.T, deps Deps) Model {
	t.Helper()
	model := NewModel(context.Background(), deps, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlD})
	if model.input.form == nil {
		t.Fatal("Ctrl-D did not open the directory form")
	}
	return model
}

func TestDirectoryFormValidatesInline(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	created := false
	deps := Deps{
		Home:            root,
		CreateDirectory: func(context.Context, string) error { created = true; return nil },
	}
	model := openDirectoryForm(t, deps)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil || !strings.Contains(model.input.form.stageError, "required") {
		t.Fatalf("empty path transition = cmd %v, error %q", cmd != nil, model.input.form.stageError)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(existing)})
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil || !strings.Contains(model.input.form.stageError, "already exists") {
		t.Fatalf("existing path transition = cmd %v, error %q", cmd != nil, model.input.form.stageError)
	}
	if created {
		t.Fatal("invalid path still created a directory")
	}
	model.SetSize(160, 20)
	if !strings.Contains(model.View(), "already exists") {
		t.Fatalf("View() = %q, want inline destination error", model.View())
	}
}

func TestDirectoryFormCancelDoesNotCreate(t *testing.T) {
	root := t.TempDir()
	created := false
	deps := Deps{
		Home:            root,
		CreateDirectory: func(context.Context, string) error { created = true; return nil },
	}
	model := openDirectoryForm(t, deps)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(filepath.Join(root, "new-dir"))})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.input.form != nil {
		t.Fatal("Escape left the directory form open")
	}
	if created {
		t.Fatal("Escape still created a directory")
	}
}

func TestDirectoryFormSuccessRefreshesAndSelects(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-dir")
	projects := []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "old"), Group: root, DisplayRemainder: "old"}}
	var created string
	deps := Deps{
		Projects:     projects,
		StartupTrace: startup.New("test", io.Discard),
		ProjectSource: func(context.Context) ([]picker.Place, error) {
			return append(append([]picker.Place(nil), projects...), picker.Place{Kind: picker.ProjectKind, Path: destination, Group: root, DisplayRemainder: "new-dir"}), nil
		},
		CreateDirectory: func(_ context.Context, path string) error { created = path; return nil },
		Home:            root,
	}
	model := openDirectoryForm(t, deps)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(destination)})
	updated, action := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if action == nil || !model.input.form.inFlight {
		t.Fatal("destination Enter did not start asynchronous creation")
	}
	updated, reload := model.Update(action())
	model = updated.(Model)
	if created != destination {
		t.Fatalf("created %q, want %q", created, destination)
	}
	if reload == nil {
		t.Fatal("directory success did not request a project refresh")
	}
	updated, _ = model.Update(reload())
	model = updated.(Model)
	if model.Cursor() != "project:"+destination {
		t.Fatalf("cursor = %q, want refreshed project", model.Cursor())
	}
}

func TestDirectoryFormOffersUnconfiguredParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "projects")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Home: root, AddProjectPath: func(context.Context, string) error { return nil }}
	form := newDirectoryForm(deps, context.Background())
	form.setFieldValue("destination", filepath.Join(parent, "new-dir"))
	result := form.validate(form)
	if result.Error != "" {
		t.Fatalf("validation error = %q, want none", result.Error)
	}
	if !strings.Contains(result.Notice, parent) || !strings.Contains(result.Notice, "not in project paths") {
		t.Fatalf("notice = %q, want parent offer for %q", result.Notice, parent)
	}
	if got := result.Values["destination"]; got != filepath.Join(parent, "new-dir") {
		t.Fatalf("resolved destination = %q", got)
	}
}

func TestDirectoryFormAddsParentOnDemand(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "projects")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "new-dir")
	var added []string
	deps := Deps{
		Home:            root,
		CreateDirectory: func(context.Context, string) error { return nil },
		AddProjectPath:  func(_ context.Context, path string) error { added = append(added, path); return nil },
	}
	model := openDirectoryForm(t, deps)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(destination)})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeySpace})
	updated, action := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if action == nil {
		t.Fatal("destination Enter did not start asynchronous creation")
	}
	model = updateModel(t, model, action())
	if model.input.form != nil {
		t.Fatal("successful creation left the modal open")
	}
	if len(added) != 1 || added[0] != parent {
		t.Fatalf("added parents = %#v, want %q", added, parent)
	}
}

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	cloneflow "github.com/vieitesss/nexo/internal/clone"
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
		CloneProject: func(_ context.Context, request cloneflow.Request) error {
			gotSource = cloneflow.SourceOperand(request.Source, request.Prefix, root)
			gotDestination = request.Destination
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
	if got, want := formValue(model.input.form, "destination"), filepath.Join(root, "repo"); got != want {
		t.Fatalf("destination default = %q, want %q", got, want)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlU})
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

func TestCloneFormDefaultsDestinationToTriggeringFolder(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	deps := Deps{
		Projects: []picker.Place{
			{Kind: picker.ProjectKind, Path: filepath.Join(root, "existing"), Group: root},
			{Kind: picker.ProjectKind, Path: filepath.Join(work, "app"), Group: work},
		},
		Home: root,
	}
	for _, test := range []struct {
		name string
		keys []tea.KeyType
		want string
	}{
		{name: "folder header", want: filepath.Join(root, "repo")},
		{name: "project row", keys: []tea.KeyType{tea.KeyRight, tea.KeyDown}, want: filepath.Join(root, "repo")},
		{name: "second header", keys: []tea.KeyType{tea.KeyDown}, want: filepath.Join(work, "repo")},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(context.Background(), deps, ProjectsTab)
			for _, key := range test.keys {
				model = updateModel(t, model, tea.KeyMsg{Type: key})
			}
			model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlO})
			if model.input.form == nil {
				t.Fatal("Ctrl-O did not open the clone form")
			}
			model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
			model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("owner/repo.git")})
			model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			if got := formValue(model.input.form, "destination"); got != test.want {
				t.Fatalf("destination default = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCloneFormKeepsTypedDestinationWhenSourceChanges(t *testing.T) {
	root := t.TempDir()
	deps := Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "existing"), Group: root}},
		Home:     root,
	}
	model := NewModel(context.Background(), deps, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("owner/one")})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model.input.form.setFieldValue("destination", filepath.Join(root, "mine"))
	model.input.form.stage = 0
	model.input.form.setFieldValue("source", "owner/two")
	model.input.form.advance()
	if got, want := formValue(model.input.form, "destination"), filepath.Join(root, "mine"); got != want {
		t.Fatalf("destination = %q, want typed %q", got, want)
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
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlU})
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

func TestCloneFormNormalizesWhitespacePrefixBeforeDefaultSelection(t *testing.T) {
	form := newCloneForm(Deps{}, context.Background(), "")
	form.setFieldValue("source", "owner/repo")
	form.setFieldValue("prefix", "   ")
	if !form.applyValidation() {
		t.Fatalf("validation error = %q", form.stageError)
	}
	if got := formValue(form, "prefix"); got != "git@github.com:" {
		t.Fatalf("prefix = %q, want configured SSH default", got)
	}
}

func TestCloneFormReplacesOppositeProtocolDefaultAfterWhitespace(t *testing.T) {
	form := newCloneForm(Deps{}, context.Background(), "")
	form.setFieldValue("protocol", "https")
	form.setFieldValue("source", "owner/repo")
	form.setFieldValue("prefix", "  git@github.com:  ")
	if !form.applyValidation() {
		t.Fatalf("validation error = %q", form.stageError)
	}
	if got := formValue(form, "prefix"); got != "https://github.com/" {
		t.Fatalf("prefix = %q, want configured HTTPS default", got)
	}
}

func mkdirForTUITest(path string) error {
	return os.MkdirAll(path, 0o755)
}

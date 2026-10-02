package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/startup"
)

func extractTestModel(t *testing.T, deps Deps) Model {
	t.Helper()
	model := NewModel(context.Background(), deps, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	return model
}

func openExtractForm(t *testing.T, model Model) Model {
	t.Helper()
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlE})
	if model.input.form == nil {
		t.Fatal("Ctrl-E did not open the extraction form")
	}
	return model
}

func TestExtractFormPrefillsSuggestionAndSubmits(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "checkout")
	var gotRepo, gotDestination, gotReturn string
	deps := Deps{
		Projects:            []picker.Place{{Kind: picker.ProjectKind, Path: repo, Group: root, Git: true, GitContext: &picker.GitContext{Identity: "feature/thing"}}},
		ExtractReturnBranch: func(context.Context, string) (string, error) { return "main", nil },
		ExtractWorktree: func(_ context.Context, repo, destination, returnBranch string) (capabilities.Worktree, error) {
			gotRepo, gotDestination, gotReturn = repo, destination, returnBranch
			return capabilities.Worktree{Path: destination, Branch: "feature/thing", ReturnBranch: returnBranch}, nil
		},
		Home: root,
	}
	model := openExtractForm(t, extractTestModel(t, deps))
	if got := formValue(model.input.form, "destination"); got != filepath.Join(root, "checkout-feature-thing") {
		t.Fatalf("destination = %q, want sibling suggestion", got)
	}
	if got := formValue(model.input.form, "return-branch"); got != "main" {
		t.Fatalf("return branch = %q, want main", got)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.input.form.stage != 1 {
		t.Fatalf("form stage = %d, want return-branch stage", model.input.form.stage)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || !model.input.form.inFlight {
		t.Fatal("return-branch Enter did not start asynchronous extraction")
	}
	model = updateModel(t, model, cmd())
	if model.input.form != nil {
		t.Fatal("successful extraction left the modal open")
	}
	if gotRepo != repo || gotDestination != filepath.Join(root, "checkout-feature-thing") || gotReturn != "main" {
		t.Fatalf("extraction = %q, %q, %q", gotRepo, gotDestination, gotReturn)
	}
}

func TestExtractFormKeepsInlineDestinationError(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "checkout"), Group: root, Git: true}},
		Home:     root,
	}
	model := openExtractForm(t, extractTestModel(t, deps))
	model.input.form.setFieldValue("destination", existing)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	want := `destination "` + existing + `" already exists`
	if cmd != nil || model.input.form == nil || model.input.form.stageError != want {
		t.Fatalf("validation = cmd %v, form=%#v; want inline %q", cmd != nil, model.input.form, want)
	}
}

func TestExtractFormPreservesTrailingSpaceRepositoryIdentity(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "checkout ")
	trimmedRepo := filepath.Join(root, "checkout")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(trimmedRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	var gotRepo string
	deps := Deps{
		Projects: []picker.Place{{Kind: picker.ProjectKind, Path: repo, Group: root, Git: true}},
		ExtractWorktree: func(_ context.Context, selectedRepo, _, _ string) (capabilities.Worktree, error) {
			gotRepo = selectedRepo
			return capabilities.Worktree{Path: filepath.Join(root, "extracted"), Branch: "feature"}, nil
		},
		Home: root,
	}
	model := openExtractForm(t, extractTestModel(t, deps))
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("extraction did not start")
	}
	updateModel(t, model, cmd())
	if gotRepo != repo {
		t.Fatalf("extraction repo = %q, want %q", gotRepo, repo)
	}
}

func TestExtractFormKeepsPreflightRefusalsInline(t *testing.T) {
	cases := []struct {
		name string
		err  string
	}{
		{name: "dirty checkout", err: "original worktree has uncommitted changes"},
		{name: "existing destination", err: "destination already exists"},
		{name: "branch checked out elsewhere", err: "branch is already checked out elsewhere"},
		{name: "no eligible return branch", err: "no eligible return branch; pass --return-branch"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			deps := Deps{
				Projects: []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "checkout"), Group: root, Git: true}},
				ExtractWorktree: func(context.Context, string, string, string) (capabilities.Worktree, error) {
					return capabilities.Worktree{}, errors.New(test.err)
				},
				Home: root,
			}
			model := openExtractForm(t, extractTestModel(t, deps))
			model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			model = updated.(Model)
			model = updateModel(t, model, cmd())
			if model.input.form == nil {
				t.Fatal("preflight refusal closed the form")
			}
			if !strings.Contains(model.input.form.stageError, test.err) {
				t.Fatalf("stage error = %q, want %q", model.input.form.stageError, test.err)
			}
		})
	}
}

func TestExtractFormCancellationIgnoresLateResult(t *testing.T) {
	root := t.TempDir()
	projects := []picker.Place{{Kind: picker.ProjectKind, Path: filepath.Join(root, "checkout"), Group: root, Git: true}}
	deps := Deps{
		Projects:     projects,
		StartupTrace: startup.New("test", io.Discard),
		ExtractWorktree: func(context.Context, string, string, string) (capabilities.Worktree, error) {
			return capabilities.Worktree{Path: filepath.Join(root, "extracted"), Branch: "feature"}, nil
		},
		Home: root,
	}
	model := openExtractForm(t, extractTestModel(t, deps))
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || !model.input.form.inFlight {
		t.Fatal("extraction did not enter in-flight state")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.input.form != nil || model.input.actionInFlight {
		t.Fatal("Escape did not cancel the in-flight extraction")
	}
	model = updateModel(t, model, cmd())
	if model.input.form != nil || model.selectAfterRefresh != "" {
		t.Fatal("late extraction result changed canceled state")
	}
}

func TestExtractSuccessRefreshesAndSelectsWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "checkout")
	destination := filepath.Join(root, "checkout-feature")
	projects := []picker.Place{{Kind: picker.ProjectKind, Path: repo, Group: root, Git: true, GitContext: &picker.GitContext{Identity: "feature"}}}
	deps := Deps{
		Projects:     projects,
		StartupTrace: startup.New("test", io.Discard),
		ProjectSource: func(context.Context) ([]picker.Place, error) {
			return append(append([]picker.Place(nil), projects...), picker.Place{Kind: picker.ProjectKind, Path: destination, Group: root, Git: true, GitContext: &picker.GitContext{Identity: "feature"}}), nil
		},
		ExtractReturnBranch: func(context.Context, string) (string, error) { return "main", nil },
		ExtractWorktree: func(context.Context, string, string, string) (capabilities.Worktree, error) {
			return capabilities.Worktree{Path: destination, Branch: "feature", ReturnBranch: "main"}, nil
		},
		Home: root,
	}
	model := openExtractForm(t, extractTestModel(t, deps))
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	updated, action := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, reload := model.Update(action())
	model = updated.(Model)
	if reload == nil {
		t.Fatal("successful extraction did not request a project refresh")
	}
	updated, _ = model.Update(reload())
	model = updated.(Model)
	if model.Cursor() != "project:"+destination {
		t.Fatalf("cursor = %q, want extracted worktree", model.Cursor())
	}
}

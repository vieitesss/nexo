package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func TestProjectTreeStartsFoldedAndFoldsFromChildren(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo"},
		{Kind: picker.ProjectKind, Path: "/work/nexo-wt", Group: "/work", DisplayRemainder: "nexo-wt", ParentPath: "/work/nexo"},
	}
	model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("empty-query default rows = %v, want folded folder", got)
	}
	if model.Cursor() != "group:/work" {
		t.Fatalf("default cursor = %q, want folder", model.Cursor())
	}

	// Left on a folded folder does nothing.
	if got := updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft}).Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("Left on folded folder changed rows: %v", got)
	}

	// Right unfolds the folder. The checkout with a worktree stays folded and
	// keeps its hidden-child count.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo"}) {
		t.Fatalf("unfolded folder rows = %v", got)
	}
	model.SetSize(80, 8)
	if !strings.Contains(shell.Plain(model.View()), "nexo  1") {
		t.Fatalf("folded checkout did not show hidden-child count: %q", shell.Plain(model.View()))
	}

	// Right on the checkout unfolds its worktree.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo", "project:/work/nexo-wt"}) {
		t.Fatalf("unfolded checkout rows = %v", got)
	}

	// Left on the worktree folds the checkout and moves the selection there.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo"}) {
		t.Fatalf("Left on worktree rows = %v", got)
	}
	if model.Cursor() != "project:/work/nexo" {
		t.Fatalf("Left on worktree cursor = %q, want checkout", model.Cursor())
	}

	// Left on a project folds the folder and moves the selection there.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("Left on project rows = %v", got)
	}
	if model.Cursor() != "group:/work" {
		t.Fatalf("Left on project cursor = %q, want folder", model.Cursor())
	}

	// An expanded folder folds itself on Left too.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("Left on expanded folder rows = %v", got)
	}
}

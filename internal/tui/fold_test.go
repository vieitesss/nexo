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

func TestProjectGroupsStartExpandedAndFoldAsAUnit(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo"},
		{Kind: picker.ProjectKind, Path: "/work/nexo-wt", Group: "/work", DisplayRemainder: "nexo-wt", ParentPath: "/work/nexo"},
	}
	model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("default expanded rows = %v, want group, project and worktree", got)
	}

	// A linked worktree folds under its own checkout without changing either
	// row's identity, and the hidden-child count remains on the checkout row.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	wantFolded := []string{"group:/work", "project:/work/nexo"}
	if got := model.Rows(); !slices.Equal(got, wantFolded) {
		t.Fatalf("folded project rows = %v, want %v", got, wantFolded)
	}
	if model.Cursor() != "project:/work/nexo" {
		t.Fatalf("folded project cursor = %q, want checkout identity", model.Cursor())
	}
	model.SetSize(80, 8)
	if !strings.Contains(shell.Plain(model.View()), "nexo  1") {
		t.Fatalf("folded project did not show hidden-child count: %q", shell.Plain(model.View()))
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo", "project:/work/nexo-wt"}) {
		t.Fatalf("unfolded project rows = %v", got)
	}

	// The containing project group still folds independently.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyUp})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("folded group rows = %v, want only header", got)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("unfolded group rows = %v, want all projects and worktrees", got)
	}
}

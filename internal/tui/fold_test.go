package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
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

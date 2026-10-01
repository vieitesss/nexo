package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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

// TestFoldLeftFoldsTheSelectedRowBeforeItsParent pins the tree-view rule: an
// expanded foldable row folds itself and keeps the cursor; every other row
// folds its parent and moves the cursor there.
func TestFoldLeftFoldsTheSelectedRowBeforeItsParent(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo"},
		{Kind: picker.ProjectKind, Path: "/work/nexo-wt1", Group: "/work", DisplayRemainder: "nexo-wt1", ParentPath: "/work/nexo"},
		{Kind: picker.ProjectKind, Path: "/work/nexo-wt2", Group: "/work", DisplayRemainder: "nexo-wt2", ParentPath: "/work/nexo"},
		{Kind: picker.ProjectKind, Path: "/work/solo", Group: "/work", DisplayRemainder: "solo"},
	}

	// An expanded folder header folds itself and stays selected.
	model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("Left on expanded folder rows = %v", got)
	}
	if model.Cursor() != "group:/work" {
		t.Fatalf("Left on expanded folder cursor = %q", model.Cursor())
	}

	// A folded project parent folds the enclosing folder and moves there.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo", "project:/work/solo"}) {
		t.Fatalf("folded project rows = %v", got)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("Left on folded project rows = %v", got)
	}
	if model.Cursor() != "group:/work" {
		t.Fatalf("Left on folded project cursor = %q", model.Cursor())
	}

	// An expanded project with worktrees folds itself and keeps the cursor.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo", "project:/work/nexo-wt1", "project:/work/nexo-wt2", "project:/work/solo"}) {
		t.Fatalf("expanded project rows = %v", got)
	}
	if model.Cursor() != "project:/work/nexo" {
		t.Fatalf("cursor after expanding project = %q", model.Cursor())
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo", "project:/work/solo"}) {
		t.Fatalf("Left on expanded project rows = %v", got)
	}
	if model.Cursor() != "project:/work/nexo" {
		t.Fatalf("Left on expanded project cursor = %q, want the project", model.Cursor())
	}

	// A worktree child folds its checkout and moves there.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo", "project:/work/solo"}) {
		t.Fatalf("Left on worktree rows = %v", got)
	}
	if model.Cursor() != "project:/work/nexo" {
		t.Fatalf("Left on worktree cursor = %q", model.Cursor())
	}

	// A plain project leaf folds the enclosing folder and moves there.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work"}) {
		t.Fatalf("Left on plain project rows = %v", got)
	}
	if model.Cursor() != "group:/work" {
		t.Fatalf("Left on plain project cursor = %q", model.Cursor())
	}
}

// TestFoldGlyphsUseOneFamilyAndStyle pins the marker family and weight: ▾ when
// expanded and ▸ when folded, rendered with the same style on folder headers
// and on projects with worktrees.
func TestFoldGlyphsUseOneFamilyAndStyle(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo"},
		{Kind: picker.ProjectKind, Path: "/work/nexo-wt", Group: "/work", DisplayRemainder: "nexo-wt", ParentPath: "/work/nexo"},
		{Kind: picker.ProjectKind, Path: "/work/solo", Group: "/work", DisplayRemainder: "solo"},
	}
	model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
	model.SetSize(80, 12)
	// Park the cursor on the leaf so the folder and checkout markers render
	// unselected and can be compared directly.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	view := model.View()
	if strings.Contains(view, "⌄") {
		t.Fatalf("view kept the old fold glyph: %q", view)
	}
	if !strings.Contains(view, muted().Render("▾")) {
		t.Fatalf("expanded folder did not use the shared ▾ style: %q", view)
	}
	if !strings.Contains(view, muted().Render("▸")) {
		t.Fatalf("folded checkout did not use the shared ▸ style: %q", view)
	}

	// Expanding the checkout shows the same ▾ style on a project row.
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyUp})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	view = model.View()
	if count := strings.Count(view, muted().Render("▾")); count < 2 {
		t.Fatalf("folder and checkout did not share one expanded glyph style (%d): %q", count, view)
	}
}

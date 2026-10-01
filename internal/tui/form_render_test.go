package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/deletion"
)

func TestDeletePreflightSmallViewportIsScrollable(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{
		{ID: "target", Title: "delete project", Fields: []formField{{ID: "path", Label: "path", Value: "/tmp/project"}}},
		{ID: "preflight", Title: "preflight"},
		{ID: "confirmation", Title: "confirm deletion", Fields: []formField{{ID: "confirm", Label: "type yes to delete", Value: "no"}}},
	}, func(*stagedForm) formValidation { return formValidation{} }, nil)
	form.deletePreflightEnabled = true
	form.stage = 1
	form.deletePlan = &deletion.Plan{Path: "/tmp/project", Action: "trash", Warnings: []deletion.Finding{
		{Code: "one", Message: "local-only Git data: unpushed commits and stashes must be reviewed"},
		{Code: "two", Message: "non-empty directory contains files that will be removed"},
		{Code: "three", Message: "remote verification failed and deletion is blocked"},
		{Code: "four", Message: "active container uses this project path"},
		{Code: "five", Message: "linked worktree has untracked files"},
	}, CanProceed: true}
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "background"
	}
	_ = overlayForm(lines, form, 32, 8)
	wrapped := strings.Join(formLinesForWidth(form.renderLines(), 24), " ")
	for _, phrase := range []string{"unpushed commits", "stashes", "contains files", "remote verification failed", "active container", "untracked files"} {
		if !strings.Contains(wrapped, phrase) {
			t.Fatalf("wrapped preflight omitted %q: %q", phrase, wrapped)
		}
	}
	if form.deletePreflightSeen {
		t.Fatal("small preflight marked seen before its findings were scrolled")
	}
	if effect := form.key(tea.KeyMsg{Type: tea.KeyEnter}); !effect.Changed || form.stage != 1 {
		t.Fatalf("Enter advanced without presenting the complete preflight: %#v stage=%d", effect, form.stage)
	}
	for !form.deletePreflightSeen {
		form.key(tea.KeyMsg{Type: tea.KeyPgDown})
		_ = overlayForm(lines, form, 32, 8)
	}
	if effect := form.key(tea.KeyMsg{Type: tea.KeyEnter}); !effect.Changed || form.stage != 2 {
		t.Fatalf("Enter after scrolling = %#v, stage=%d; want confirmation stage", effect, form.stage)
	}
}

func TestDeleteConfirmationKeepsActiveFieldVisibleWhenNarrow(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{ID: "confirmation", Title: "confirm deletion", Fields: []formField{
		{ID: "mode", Label: "mode (trash/permanent)", Value: "permanent"},
		{ID: "force", Label: "accept warnings (yes/no)", Value: "yes"},
		{ID: "confirm", Label: "type yes to delete", Value: "yes"},
	}}}, nil, nil)
	form.field = 2
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "background"
	}
	_ = overlayForm(lines, form, 28, 7)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "yes") {
		t.Fatalf("confirmation value disappeared at narrow size: %q", joined)
	}
	for _, line := range formLinesForWidth(form.renderLines(), 20) {
		if strings.Contains(line, "type yes to delete") && !strings.Contains(line, "yes") {
			t.Fatalf("edited value hidden by long label: %q", line)
		}
	}
}

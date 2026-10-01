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
		{Code: "one", Message: "warning one"},
		{Code: "two", Message: "warning two"},
		{Code: "three", Message: "warning three"},
		{Code: "four", Message: "warning four"},
		{Code: "five", Message: "warning five"},
	}, CanProceed: true}
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "background"
	}
	_ = overlayForm(lines, form, 32, 8)
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

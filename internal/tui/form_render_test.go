package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/deletion"
	"github.com/vieitesss/nexo/internal/shell"
)

func TestCompletedSummaryFitsDialogInnerWidth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "scratchpad", "verify", "forms", "sandbox", "home", "src", "gamma")
	form := newStagedForm(context.Background(), []formStage{
		{ID: "target", Title: "\x1b[1mdelete project\x1b[0m", Fields: []formField{{ID: "path", Label: "path", Kind: formDisplay, Default: path, AbbreviateHome: true}}},
		{ID: "next", Title: "next"},
	}, nil, nil)
	form.stage = 1
	for _, width := range []int{80, 160} {
		lines := overlayForm(make([]string, 30), form, width, 30)
		matches := []string{}
		for _, line := range lines {
			if strings.Contains(line, "delete project") {
				matches = append(matches, line)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("width %d summary lines = %d, want one: %#v", width, len(matches), lines)
		}
		if got := shell.StringWidth(matches[0]); got > width {
			t.Fatalf("width %d rendered summary width = %d, want <= terminal width: %q", width, got, matches[0])
		}
		if strings.Contains(strings.Join(lines, "\n"), "│ …/") {
			t.Fatalf("width %d wrapped completed summary: %#v", width, lines)
		}
	}
}

func TestSummaryAbbreviatesHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	form := newStagedForm(context.Background(), []formStage{
		{ID: "target", Title: "delete project", Fields: []formField{{ID: "path", Label: "path", Kind: formDisplay, Default: filepath.Join(home, "src"), AbbreviateHome: true}}},
		{ID: "next", Title: "next"},
	}, nil, nil)
	form.stage = 1
	if got := shell.Plain(strings.Join(form.renderLines(), "\n")); !strings.Contains(got, "delete project  ~/src") {
		t.Fatalf("summary path = %q, want ~/src", got)
	}
}

func TestDeletePreflightSmallViewportIsScrollable(t *testing.T) {
	delete := newDeleteForm(Deps{}, context.Background(), "/tmp/project")
	form := delete.stagedForm
	form.stage = delete.preflightStage()
	delete.plan = &deletion.Plan{Path: "/tmp/project", Action: "trash", Warnings: []deletion.Finding{
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
	if delete.preflightSeen {
		t.Fatal("small preflight marked seen before its findings were scrolled")
	}
	if effect := form.key(tea.KeyMsg{Type: tea.KeyEnter}); !effect.Changed || form.stage != 1 {
		t.Fatalf("Enter advanced without presenting the complete preflight: %#v stage=%d", effect, form.stage)
	}
	for !delete.preflightSeen {
		form.key(tea.KeyMsg{Type: tea.KeyPgDown})
		_ = overlayForm(lines, form, 32, 8)
	}
	if effect := form.key(tea.KeyMsg{Type: tea.KeyEnter}); !effect.Changed || form.stage != 2 {
		t.Fatalf("Enter after scrolling = %#v, stage=%d; want confirmation stage", effect, form.stage)
	}
}

func TestFormWrapHardBreaksAnsiWordsAndBoundsIndentation(t *testing.T) {
	lines := formLinesForWidth([]string{
		"    verylongaliaswithnobreaks",
		"warning: \x1b[31mverylongpathwithnobreaks\x1b[0m",
	}, 8)
	if len(lines) < 4 {
		t.Fatalf("wrapped lines = %#v, want hard-wrapped content", lines)
	}
	for _, line := range lines {
		if width := shell.StringWidth(line); width > 8 {
			t.Fatalf("line width = %d, want <= 8: %q", width, line)
		}
	}
	joined := strings.ReplaceAll(shell.Plain(strings.Join(lines, " ")), " ", "")
	if !strings.Contains(joined, "verylongalias") || !strings.Contains(joined, "verylongpath") {
		t.Fatalf("wrapped content lost: %q", joined)
	}
}

func TestDeleteConfirmationKeepsActiveFieldVisibleWhenNarrow(t *testing.T) {
	form := newStagedForm(context.Background(), []formStage{{ID: "confirmation", Title: "confirm deletion", Fields: []formField{
		{ID: "mode", Label: "mode (trash/permanent)", Kind: formChoice, Options: []string{"trash", "permanent"}, Default: "permanent"},
		{ID: "force", Label: "accept warnings (yes/no)", Kind: formToggle, Default: "yes"},
		{ID: "confirm", Label: "type yes to delete", Kind: formText, Default: "yes"},
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

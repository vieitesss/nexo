package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func projectFixture() []picker.Place {
	return []picker.Place{
		{Kind: picker.ProjectKind, Path: "/home/tester/work/prefapp/gitops-k8s", Group: "/home/tester/work/prefapp", DisplayRemainder: "gitops-k8s", Git: true, GitContext: &picker.GitContext{Identity: "main", Dirty: true}},
		{Kind: picker.ProjectKind, Path: "/home/tester/work/prefapp/gitops-fsforge-improvements", Group: "/home/tester/work/prefapp", DisplayRemainder: "gitops-fsforge-improvements", ParentPath: "/home/tester/work/prefapp/gitops-k8s", Git: true, GitContext: &picker.GitContext{Identity: "gitops-fsforge-improvements"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/work/prefapp/gitops-publication-si-01-03-jh69pd38vg", Group: "/home/tester/work/prefapp", DisplayRemainder: "gitops-publication-si-01-03-jh69pd38vg", ParentPath: "/home/tester/work/prefapp/gitops-k8s", Git: true, GitContext: &picker.GitContext{Identity: "fix/publication-si-01-03-jh69pd38vg", Adds: 277, Deletes: 376}},
		{Kind: picker.ProjectKind, Path: "/home/tester/work/prefapp/gitops-readme-diagram", Group: "/home/tester/work/prefapp", DisplayRemainder: "gitops-readme-diagram", ParentPath: "/home/tester/work/prefapp/gitops-k8s", Git: true, GitContext: &picker.GitContext{Identity: "gitops-readme-diagram"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/work/firestartr-pro/srv-github-app-generator", Group: "/home/tester/work/firestartr-pro", DisplayRemainder: "srv-github-app-generator", Git: true, GitContext: &picker.GitContext{Identity: "main", Dirty: true}},
		{Kind: picker.ProjectKind, Path: "/home/tester/work/other/unrelated", Group: "/home/tester/work/other", DisplayRemainder: "unrelated", SearchText: "/home/tester/work/gitops-scattered-path", Git: true, GitContext: &picker.GitContext{Identity: "gitops-branch-only"}},
	}
}

func TestProjectsGroupedRowsUseHomeAndWorktreeGutter(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetSize(120, 36)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "~/work/prefapp") {
		t.Fatalf("group path was not abbreviated: %q", plain)
	}
	if strings.Contains(plain, "/home/tester/work/prefapp") {
		t.Fatalf("group path retained home prefix: %q", plain)
	}
	if !strings.Contains(plain, "├ gitops-fsforge-improvements") || !strings.Contains(plain, "└ gitops-readme-diagram") {
		t.Fatalf("worktree gutters missing: %q", plain)
	}
	for _, styledLine := range strings.Split(model.View(), "\n") {
		line := shell.Plain(styledLine)
		if strings.Contains(line, "gitops-") && strings.TrimSpace(line) != "" && shell.StringWidth(line) != 120 {
			t.Fatalf("project line width = %d: %q", shell.StringWidth(line), line)
		}
	}
}

func TestProjectsColumnsRespectTerminalWidths(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	for _, width := range []int{60, 80, 120} {
		model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
		model.SetSize(width, 16)
		for line, styledValue := range strings.Split(model.View(), "\n") {
			value := shell.Plain(styledValue)
			if got := shell.StringWidth(value); got != width {
				t.Fatalf("width %d line %d rendered %d: %q", width, line, got, value)
			}
		}
	}
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetSize(120, 16)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "gitops-publication-si-01-03-jh69pd38vg") || !strings.Contains(plain, "fix/publication-si-01-03-jh69pd38vg") {
		t.Fatalf("wide columns truncated needlessly: %q", plain)
	}
}

func TestProjectsQueryIsFlatRankedAndStartsAtBestName(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetQuery("gitops")
	rows := model.Rows()
	if len(rows) != 5 {
		t.Fatalf("query rows = %v, want matching names and branch/path result", rows)
	}
	for _, id := range rows {
		if strings.HasPrefix(id, "group:") {
			t.Fatalf("flat query retained group header: %v", rows)
		}
	}
	if model.Cursor() != rows[0] || rows[0] != "project:/home/tester/work/prefapp/gitops-fsforge-improvements" {
		t.Fatalf("cursor/ordering = %q/%v, want best name first with name tie-break", model.Cursor(), rows)
	}
	model.SetSize(80, 16)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "~/work/prefapp") || strings.Contains(plain, "⌄  ") {
		t.Fatalf("flat query did not move folder to final column: %q", plain)
	}
}

func TestProjectsLoadingAndDetachedBranchStates(t *testing.T) {
	places := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/home/tester/work/loading", Group: "/home/tester/work", DisplayRemainder: "loading", Git: true},
		{Kind: picker.ProjectKind, Path: "/home/tester/work/detached", Group: "/home/tester/work", DisplayRemainder: "detached", Git: true, GitContext: &picker.GitContext{Identity: "abc1234"}},
	}
	model := NewModel(context.Background(), Deps{Projects: places}, ProjectsTab)
	model.SetSize(80, 10)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "loading") || !strings.Contains(plain, "…") {
		t.Fatalf("loading branch state missing: %q", plain)
	}
	if !strings.Contains(plain, "⎇ abc1234") {
		t.Fatalf("detached short sha missing: %q", plain)
	}
}

func TestSelectionAccentOccupiesOneBodyLineForEveryRowKind(t *testing.T) {
	deps := Deps{
		Projects:   projectFixture(),
		Containers: []capabilities.Container{{ID: "one", Name: "one", Machine: picker.LocalMachine()}},
		Agents:     []capabilities.Agent{{PaneID: "pane", ContainerID: "one", Container: "one", Harness: "pi", Target: "one:0.1", State: "working"}},
	}
	for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
		model := NewModel(context.Background(), deps, tab)
		model.SetSize(100, 30)
		for _, id := range model.Rows() {
			model.cursor = id
			styledLines := strings.Split(model.View(), "\n")
			body := styledLines[2:29]
			marked := 0
			for _, line := range body {
				if strings.Contains(line, "▌") {
					marked++
				}
			}
			if marked != 1 {
				t.Fatalf("tab %s row %q has accent on %d lines", tab, id, marked)
			}
		}
	}
}

package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
	// Prefix matches tie on score. The shorter name wins, so gitops-k8s
	// leads gitops-fsforge-improvements; the branch-only match stays last.
	want := []string{
		"project:/home/tester/work/prefapp/gitops-k8s",
		"project:/home/tester/work/prefapp/gitops-readme-diagram",
		"project:/home/tester/work/prefapp/gitops-fsforge-improvements",
		"project:/home/tester/work/prefapp/gitops-publication-si-01-03-jh69pd38vg",
		"project:/home/tester/work/other/unrelated",
	}
	rows := model.Rows()
	if !slices.Equal(rows, want) {
		t.Fatalf("ranked rows = %v, want %v", rows, want)
	}
	for _, id := range rows {
		if strings.HasPrefix(id, "group:") {
			t.Fatalf("flat query retained group header: %v", rows)
		}
	}
	if model.Cursor() != rows[0] {
		t.Fatalf("cursor = %q, want first ranked row %q", model.Cursor(), rows[0])
	}
	// The folder column is flat mode only and at 80 columns keeping it would
	// starve the branch, so assert its placement at a width where it survives.
	model.SetSize(120, 16)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "~/work/prefapp") || strings.Contains(plain, "⌄  ") {
		t.Fatalf("flat query did not move folder to final column: %q", plain)
	}
}

func TestProjectsFlatRankingTieBreaksShorterThenRootThenName(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	places := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/home/tester/w/parent", Group: "/home/tester/w", DisplayRemainder: "other", Git: true, GitContext: &picker.GitContext{Identity: "other"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/w/parent-wt", Group: "/home/tester/w", DisplayRemainder: "alpha", ParentPath: "/home/tester/w/parent", Git: true, GitContext: &picker.GitContext{Identity: "alpha-wt"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/w/rootx", Group: "/home/tester/w", DisplayRemainder: "alpha", Git: true, GitContext: &picker.GitContext{Identity: "alpha"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/w/tool", Group: "/home/tester/w", DisplayRemainder: "tool", Git: true, GitContext: &picker.GitContext{Identity: "main"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/w/tool-long", Group: "/home/tester/w", DisplayRemainder: "tool-long", Git: true, GitContext: &picker.GitContext{Identity: "tool-long"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/w/beta", Group: "/home/tester/w", DisplayRemainder: "beta", Git: true, GitContext: &picker.GitContext{Identity: "beta"}},
		{Kind: picker.ProjectKind, Path: "/home/tester/w/bexa", Group: "/home/tester/w", DisplayRemainder: "bexa", Git: true, GitContext: &picker.GitContext{Identity: "bexa"}},
	}
	model := NewModel(context.Background(), Deps{Projects: places}, ProjectsTab)

	model.SetQuery("tool")
	if got, want := model.Rows(), []string{"project:/home/tester/w/tool", "project:/home/tester/w/tool-long"}; !slices.Equal(got, want) {
		t.Fatalf("shorter-name tie-break = %v, want %v", got, want)
	}

	model.SetQuery("alpha")
	if got, want := model.Rows(), []string{"project:/home/tester/w/rootx", "project:/home/tester/w/parent-wt"}; !slices.Equal(got, want) {
		t.Fatalf("root-before-worktree tie-break = %v, want %v", got, want)
	}

	model.SetQuery("be")
	if got, want := model.Rows(), []string{"project:/home/tester/w/beta", "project:/home/tester/w/bexa"}; !slices.Equal(got, want) {
		t.Fatalf("name tie-break = %v, want %v", got, want)
	}
}

func TestProjectsQueryKeepsSelectionWhileRefining(t *testing.T) {
	places := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/alpha", Group: "/work", DisplayRemainder: "alpha"},
		{Kind: picker.ProjectKind, Path: "/work/alphabet", Group: "/work", DisplayRemainder: "alphabet"},
		{Kind: picker.ProjectKind, Path: "/work/about", Group: "/work", DisplayRemainder: "about"},
	}
	model := NewModel(context.Background(), Deps{Projects: places}, ProjectsTab)
	model.SetQuery("a")
	if model.Cursor() != "project:/work/about" {
		t.Fatalf("empty-to-query cursor = %q, want best match", model.Cursor())
	}
	model.cursor = "project:/work/alphabet"
	model.SetQuery("al")
	if model.Cursor() != "project:/work/alphabet" {
		t.Fatalf("refined query lost matching selection: %q", model.Cursor())
	}
	model.cursor = "project:/work/alpha"
	model.SetQuery("ab")
	if model.Cursor() != "project:/work/about" {
		t.Fatalf("refined query did not choose best replacement: %q", model.Cursor())
	}
}

func TestProjectsQueryPreservesFoldState(t *testing.T) {
	places := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo"},
		{Kind: picker.ProjectKind, Path: "/work/nexo-wt", Group: "/work", DisplayRemainder: "nexo-wt", ParentPath: "/work/nexo"},
	}
	model := NewModel(context.Background(), Deps{Projects: places}, ProjectsTab)
	model = updateModel(t, model, keyMsg(tea.KeyLeft))
	model.SetQuery("nexo")
	model.SetQuery("")
	if got := model.Rows(); !slices.Equal(got, []string{"group:/work", "project:/work/nexo"}) {
		t.Fatalf("query change reset project fold: %v", got)
	}
}

func TestProjectsFlatNaturalNameWidthWhenRowFits(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	name := strings.Repeat("n", 40)
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/" + name, Group: "/work", DisplayRemainder: name, Git: true, GitContext: &picker.GitContext{Identity: "main"}},
	}}, ProjectsTab)
	model.SetQuery("n")
	model.SetSize(80, 10)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, name) || !strings.Contains(plain, "main") {
		t.Fatalf("fitting row was truncated: %q", plain)
	}
}

func TestProjectBranchAndFolderMatchesAreHighlighted(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	branch := NewModel(context.Background(), Deps{Projects: []picker.Place{{
		Kind: picker.ProjectKind, Path: "/work/unrelated", Group: "/work/folder", DisplayRemainder: "unrelated", Git: true,
		GitContext: &picker.GitContext{Identity: "unique-branch"},
	}}}, ProjectsTab)
	branch.SetQuery("unique")
	branch.SetSize(120, 8)
	if !strings.Contains(branch.View(), "\x1b[1;33mu") {
		t.Fatalf("branch match was not highlighted: %q", branch.View())
	}

	folder := NewModel(context.Background(), Deps{Projects: []picker.Place{{
		Kind: picker.ProjectKind, Path: "/work/unrelated", Group: "/home/tester/folder", DisplayRemainder: "unrelated", SearchText: "/folder", Git: true,
	}}}, ProjectsTab)
	folder.SetQuery("folder")
	folder.SetSize(120, 8)
	if !strings.Contains(folder.View(), "\x1b[1;33mf") {
		t.Fatalf("folder match was not highlighted: %q", folder.View())
	}
}

func TestProjectsFlatRowFitsWithoutTruncation(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetQuery("gitops")
	// 106 is the natural width of the widest flat row in this fixture.
	model.SetSize(106, 24)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "gitops-publication-si-01-03-jh69pd38vg") {
		t.Fatalf("name truncated although the row fits: %q", plain)
	}
	if !strings.Contains(plain, "fix/publication-si-01-03-jh69pd38vg") {
		t.Fatalf("branch truncated although the row fits: %q", plain)
	}
}

func TestProjectsFlatNameLeadsBranchAt80(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetQuery("gitops")
	model.SetSize(80, 24)
	layout := model.projectLayout()
	if minName := 16; layout.name < minName {
		t.Fatalf("name width %d fell below the floor %d", layout.name, minName)
	}
	if layout.name <= layout.branch {
		t.Fatalf("name width %d did not lead branch width %d: %+v", layout.name, layout.branch, layout)
	}
	if cap := int(0.45 * float64(80)); layout.name != cap {
		t.Fatalf("name width %d != cap %d: %+v", layout.name, cap, layout)
	}
}

// TestProjectsFlatKeepsNamesUpTo30At80 pins the priority change: at a narrow
// terminal every visible name that fits the name cap must render in full,
// even when that means the branch and folder lose cells instead.
func TestProjectsFlatKeepsNamesUpTo30At80(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetQuery("gitops")
	model.SetSize(80, 24)
	layout := model.projectLayout()
	shown := 0
	for _, row := range model.rows {
		if row.kind != itemRow || row.itemKind != "project" {
			continue
		}
		name := row.treePrefix + projectLabel(row.place)
		if shell.StringWidth(name) > 30 {
			continue
		}
		shown++
		if shell.StringWidth(name) > layout.name {
			t.Fatalf("name %q (%d cells) exceeds the %d-cell name column at 80 columns: %+v", name, shell.StringWidth(name), layout.name, layout)
		}
	}
	if shown < 3 {
		t.Fatalf("fixture stopped exercising short names: %d", shown)
	}
}

func TestProjectsFlatDropsFolderBeforeCuttingBranch(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetQuery("gitops")
	model.SetSize(60, 16)
	layout := model.projectLayout()
	if layout.path != 0 {
		t.Fatalf("folder column kept at 60 columns: %+v", layout)
	}
	if minBranch := 12; layout.branch < minBranch {
		t.Fatalf("branch width %d fell below the floor %d", layout.branch, minBranch)
	}
	plain := shell.Plain(model.View())
	if strings.Contains(plain, "~/work/prefapp") {
		t.Fatalf("dropped folder column still rendered: %q", plain)
	}
}

func TestProjectsGroupedColumnsUseTheFullWidthAt60(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetSize(60, 16)
	layout := model.projectLayout()
	if minBranch := 12; layout.branch < minBranch {
		t.Fatalf("grouped branch width %d fell below the floor %d", layout.branch, minBranch)
	}
	used := layout.name + layout.branch + layout.summary + 2 + 2*2
	if used != 60 {
		t.Fatalf("grouped row uses %d of 60 cells: %+v", used, layout)
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

package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func richDeps() Deps {
	since := time.Now().Add(-3 * time.Minute)
	local := picker.LocalMachine()
	return Deps{
		Projects: []picker.Place{
			{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo", Git: true},
			{Kind: picker.ProjectKind, Path: "/work/other", Group: "/work", DisplayRemainder: "other", Git: true},
			{Kind: picker.ProjectKind, Path: "/work/nexo-wt", Group: "/work", DisplayRemainder: "nexo-wt", ParentPath: "/work/nexo", Git: true},
		},
		Containers: []capabilities.Container{
			{ID: "local-one", Name: "one", Machine: local, Preview: "shell prompt"},
			{ID: "remote-two", Name: "two", Machine: "remote"},
		},
		Agents: []capabilities.Agent{
			{PaneID: "need", Target: "one:0.1", ContainerID: "local-one", Container: "one", Harness: "pi", State: "stopped", StoppedSince: &since},
			{PaneID: "work", Target: "remote/two:0.1", ContainerID: "remote-two", Container: "two", Harness: "claude", State: "working", Branch: "main"},
		},
		RemoteMachines: []string{"remote-loading"},
	}
}

func noBackgroundSGR(t *testing.T, frame string) {
	t.Helper()
	re := regexp.MustCompile(`\x1b\[([0-9;:]*)m`)
	for _, match := range re.FindAllStringSubmatch(frame, -1) {
		for _, parameter := range strings.FieldsFunc(match[1], func(r rune) bool { return r == ';' || r == ':' }) {
			if parameter == "" {
				continue
			}
			var value int
			if _, err := fmt.Sscanf(parameter, "%d", &value); err != nil {
				continue
			}
			if value == 7 || value == 48 || (value >= 40 && value <= 47) || (value >= 100 && value <= 107) {
				t.Fatalf("frame contains background SGR %q in %q", match[0], frame)
			}
		}
	}
}

func frameFor(t *testing.T, model Model, tab Tab) string {
	t.Helper()
	model.switchTo(tab)
	model.SetSize(100, 30)
	frame := model.View()
	noBackgroundSGR(t, frame)
	return frame
}

func TestNoBackgroundSGRForEveryTabAndState(t *testing.T) {
	deps := richDeps()
	deps.Containers[0].Name = "\x1b[41mone\x1b[0m"
	deps.Containers[0].Preview = "\x1b[41mred\x1b[0m \x1b[7mreverse\x1b[0m \x1b[48;5;200mhostile\x1b[0m"
	deps.Projects[0].DisplayRemainder = "\x1b[41mnexo\x1b[0m"
	deps.Agents[0].Harness = "\x1b[41mpi\x1b[0m"
	deps.Agents[0].Branch = "\x1b[7mmain\x1b[0m"
	states := []func(*Model){

		func(*Model) {},
		func(m *Model) { m.SetQuery("o") },
		func(m *Model) { m.toggleMark() },
		func(m *Model) { m.confirmKill = true },
		func(m *Model) { m.renameMode = true },
		func(m *Model) { m.SetToast("updated") },
	}
	for _, profile := range []termenv.Profile{termenv.ANSI, termenv.ANSI256, termenv.TrueColor} {
		lipgloss.SetColorProfile(profile)
		for _, prepare := range states {
			for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
				copy := NewModel(context.Background(), deps, ContainersTab)
				prepare(&copy)
				frameFor(t, copy, tab)
			}
		}
	}
	loading := NewModel(context.Background(), richDeps(), ContainersTab)
	frameFor(t, loading, ContainersTab)
	loading = updateModel(t, loading, picker.RemoteBatch{Machine: "remote-loading"})
	frameFor(t, loading, ContainersTab)
	loading = updateModel(t, loading, picker.RemoteBatch{Machine: "remote-loading", Error: "\x1b[41moffline\x1b[0m"})
	frameFor(t, loading, ContainersTab)
}

func TestEveryPrintableKeyIsSearchInput(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	want := []rune{}
	for value := rune(32); value <= 126; value++ {
		want = append(want, value)
		model = updateModel(t, model, keyMsg(tea.KeyRunes, value))
	}
	if got := []rune(model.Query()); string(got) != string(want) {
		t.Fatalf("query did not retain printable input: got %q want %q", model.Query(), string(want))
	}
}

func TestProjectQueryStartsAtMatchingItem(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ProjectsTab)
	model.SetQuery("nexo-wt")
	if !strings.HasPrefix(model.Cursor(), "project:") || model.Cursor() == "group:/work" {
		t.Fatalf("cursor = %q, want the matching project, not group header", model.Cursor())
	}
}

func TestOnlyProjectsFold(t *testing.T) {
	projects := NewModel(context.Background(), richDeps(), ProjectsTab)
	projects = updateModel(t, projects, keyMsg(tea.KeyRight)) // group
	if len(projects.Rows()) != 3 {
		t.Fatalf("group expansion rows = %v", projects.Rows())
	}
	projects = updateModel(t, projects, keyMsg(tea.KeyDown))  // root
	projects = updateModel(t, projects, keyMsg(tea.KeyRight)) // linked worktree
	if len(projects.Rows()) != 4 {
		t.Fatalf("worktree expansion rows = %v", projects.Rows())
	}
	containers := NewModel(context.Background(), richDeps(), ContainersTab)
	containers = updateModel(t, containers, keyMsg(tea.KeyUp))
	before := strings.Join(containers.Rows(), "\x00")
	containers = updateModel(t, containers, keyMsg(tea.KeyRight))
	if got := strings.Join(containers.Rows(), "\x00"); got != before {
		t.Fatalf("machine group folded: %v", containers.Rows())
	}
}

func TestTabKeysAndQuerySwitching(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyTab))
	if model.Tab() != ProjectsTab {
		t.Fatalf("Tab selected %q", model.Tab())
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.Tab() != ContainersTab {
		t.Fatalf("Shift-Tab selected %q", model.Tab())
	}
	for key, want := range map[tea.KeyType]Tab{tea.KeyF1: AgentsTab, tea.KeyF2: ContainersTab, tea.KeyF3: ProjectsTab} {
		model = updateModel(t, model, tea.KeyMsg{Type: key})
		if model.Tab() != want {
			t.Fatalf("key %v selected %q, want %q", key, model.Tab(), want)
		}
	}
	auto := NewModel(context.Background(), richDeps(), AgentsTab)
	auto.SetQuery("nexo")
	if auto.Tab() != ProjectsTab || auto.Query() != "nexo" {
		t.Fatalf("zero-match query did not switch tabs: %q/%q", auto.Tab(), auto.Query())
	}
	model.SetQuery("nexo")
	if model.Query() != "nexo" || model.Tab() != ProjectsTab {
		t.Fatalf("query/tab = %q/%q", model.Query(), model.Tab())
	}
	model = updateModel(t, model, keyMsg(tea.KeyF2))
	if model.Query() != "nexo" {
		t.Fatal("switching tabs lost query")
	}
	if model.Tab() != AgentsTab && model.Tab() != ContainersTab && model.Tab() != ProjectsTab {
		t.Fatalf("invalid tab %q", model.Tab())
	}
}

func TestActionKeysAndEsc(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyCtrlT))
	if len(model.Marked()) != 1 {
		t.Fatalf("Ctrl-T marks = %v", model.Marked())
	}
	model = updateModel(t, model, keyMsg(tea.KeyCtrlX))
	if !model.confirmKill {
		t.Fatal("Ctrl-X did not enter confirmation")
	}
	model = updateModel(t, model, keyMsg(tea.KeyRunes, 'n'))
	if model.confirmKill {
		t.Fatal("n did not cancel kill")
	}
	model = updateModel(t, model, keyMsg(tea.KeyCtrlR))
	if !model.renameMode {
		t.Fatal("Ctrl-R did not enter rename")
	}
	for _, value := range []rune{'n', 'e', 'w'} {
		model = updateModel(t, model, keyMsg(tea.KeyRunes, value))
	}
	model = updateModel(t, model, keyMsg(tea.KeyEnter))
	if _, _, ok := model.RenameTargetRequest(); !ok {
		t.Fatal("Enter did not accept rename")
	}
	openProject := NewModel(context.Background(), richDeps(), ProjectsTab)
	openProject.SetQuery("nexo")
	openProject = updateModel(t, openProject, keyMsg(tea.KeyEnter))
	if _, ok := openProject.SelectedPlace(); !ok {
		t.Fatal("Enter did not select project")
	}
	openAgent := NewModel(context.Background(), richDeps(), AgentsTab)
	openAgent = updateModel(t, openAgent, keyMsg(tea.KeyEnter))
	if _, ok := openAgent.SelectedAgent(); !ok {
		t.Fatal("Enter did not select agent")
	}
	query := NewModel(context.Background(), richDeps(), ContainersTab)
	query.SetQuery("one")
	query = updateModel(t, query, keyMsg(tea.KeyEsc))
	if query.Query() != "" || query.Cancelled() {
		t.Fatal("first Esc did not only clear query")
	}
	query = updateModel(t, query, keyMsg(tea.KeyEsc))
	if !query.Cancelled() {
		t.Fatal("second Esc did not close")
	}
}

func TestSelectionIdentityAcrossResizeAndAsyncBatches(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ContainersTab)
	identity := model.Cursor()
	for _, size := range [][2]int{{200, 50}, {40, 6}, {80, 4}} {
		model.SetSize(size[0], size[1])
		if model.Cursor() != identity {
			t.Fatal("resize changed selection identity")
		}
	}
	model = updateModel(t, model, picker.PreviewBatch{Previews: map[picker.ContainerRef]string{{Machine: picker.LocalMachine(), ID: "local-one"}: "new preview"}})
	if model.Cursor() != identity {
		t.Fatal("preview changed selection identity")
	}
	model = updateModel(t, model, agentsMsg{agents: richDeps().Agents})
	if model.Cursor() != identity {
		t.Fatal("agent refresh changed selection identity")
	}
	model = updateModel(t, model, picker.RemoteBatch{Machine: "remote", Places: picker.ContainerPlaces([]capabilities.Container{{ID: "remote-three", Name: "three", Machine: "remote"}})})
	if model.Cursor() != identity {
		t.Fatal("remote batch changed selection identity")
	}
}

func TestSizeContractForEveryTab(t *testing.T) {
	for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
		for _, size := range [][2]int{{200, 50}, {100, 30}, {80, 24}, {60, 16}, {40, 6}, {80, 4}} {
			model := NewModel(context.Background(), richDeps(), tab)
			model.SetSize(size[0], size[1])
			lines := strings.Split(model.View(), "\n")
			if len(lines) != size[1] {
				t.Fatalf("%q %dx%d rendered %d lines", tab, size[0], size[1], len(lines))
			}
			for line, value := range lines {
				if width := shell.StringWidth(value); width != size[0] {
					t.Fatalf("%q %dx%d line %d width %d", tab, size[0], size[1], line, width)
				}
			}
		}
	}
}

func TestFooterOnlyAdvertisesCurrentActions(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), ProjectsTab)
	model.SetSize(100, 10)
	groupFooter := shell.Plain(model.footer())
	if strings.Contains(groupFooter, "Enter") || strings.Contains(groupFooter, "Ctrl-R") {
		t.Fatalf("group footer advertises unavailable action: %q", groupFooter)
	}
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	model = updateModel(t, model, keyMsg(tea.KeyDown))
	projectFooter := shell.Plain(model.footer())
	if !strings.Contains(projectFooter, "Enter") || !strings.Contains(projectFooter, "fold") {
		t.Fatalf("project footer missing actions: %q", projectFooter)
	}
	model = NewModel(context.Background(), richDeps(), ContainersTab)
	model.SetSize(100, 10)
	containerFooter := shell.Plain(model.footer())
	for _, action := range []string{"Enter", "Ctrl-X", "Ctrl-R", "Ctrl-T"} {
		if !strings.Contains(containerFooter, action) {
			t.Fatalf("container footer missing %q: %q", action, containerFooter)
		}
	}
}

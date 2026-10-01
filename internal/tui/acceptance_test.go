package tui

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// backgroundSGRs returns every SGR escape in value that paints a background,
// skipping the payload of extended colours so a 256-colour or truecolour
// foreground component is never mistaken for a background code.
func backgroundSGRs(value string) []string {
	found := []string{}
	for index := 0; index < len(value); {
		if value[index] != '\x1b' || index+1 >= len(value) || value[index+1] != '[' {
			index++
			continue
		}
		end := strings.IndexByte(value[index+2:], 'm')
		if end < 0 {
			break
		}
		end += index + 2
		if sgrPaintsBackground(value[index+2 : end]) {
			found = append(found, value[index:end+1])
		}
		index = end + 1
	}
	return found
}

func sgrPaintsBackground(params string) bool {
	parts := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for position := 0; position < len(parts); position++ {
		code, err := strconv.Atoi(parts[position])
		if err != nil {
			continue
		}
		switch {
		case code == 38 || code == 48:
			if position+1 < len(parts) {
				switch parts[position+1] {
				case "5":
					position += 2
				case "2":
					position += 4
				}
			}
			if code == 48 {
				return true
			}
		case code == 7, code == 49, code >= 40 && code <= 47, code >= 100 && code <= 107:
			return true
		}
	}
	return false
}

func noBackgroundSGR(t *testing.T, frame string) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	for lineNumber, line := range lines {
		if len(backgroundSGRs(line)) == 0 {
			continue
		}
		if lineNumber < 2 || lineNumber == len(lines)-1 {
			t.Fatalf("frame contains an unexpected background SGR on line %d in %q", lineNumber, frame)
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
	if got := projects.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("folders start folded, rows = %v", projects.Rows())
	}
	// Left on a folded folder does nothing; Right unfolds it.
	projects = updateModel(t, projects, keyMsg(tea.KeyLeft))
	if got := projects.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("Left on folded folder changed rows: %v", got)
	}
	projects = updateModel(t, projects, keyMsg(tea.KeyRight))
	if got := projects.Rows(); len(got) != 3 || got[1] != "project:/work/nexo" || got[2] != "project:/work/other" {
		t.Fatalf("folder did not unfold to folded checkouts: %v", got)
	}
	// Right on the checkout reveals its worktree.
	projects = updateModel(t, projects, keyMsg(tea.KeyDown))
	projects = updateModel(t, projects, keyMsg(tea.KeyRight))
	if got := len(projects.Rows()); got != 4 {
		t.Fatalf("checkout did not unfold with its worktree: %v", projects.Rows())
	}
	// Left on the worktree folds the checkout and moves the selection there.
	projects = updateModel(t, projects, keyMsg(tea.KeyDown))
	projects = updateModel(t, projects, keyMsg(tea.KeyLeft))
	if got := projects.Rows(); len(got) != 3 || got[1] != "project:/work/nexo" {
		t.Fatalf("worktree did not fold under checkout: %v", got)
	}
	if projects.Cursor() != "project:/work/nexo" {
		t.Fatalf("Left on worktree cursor = %q", projects.Cursor())
	}
	// Left on the checkout folds the folder.
	projects = updateModel(t, projects, keyMsg(tea.KeyLeft))
	if got := projects.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("checkout did not fold folder: %v", got)
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
	model.deps.RenameContainer = func(context.Context, picker.ContainerRef, string) error { return nil }
	model = updateModel(t, model, keyMsg(tea.KeyCtrlR))
	if !model.renameMode {
		t.Fatal("Ctrl-R did not enter rename")
	}
	for _, value := range []rune{'n', 'e', 'w'} {
		model = updateModel(t, model, keyMsg(tea.KeyRunes, value))
	}
	updated, cmd := model.Update(keyMsg(tea.KeyEnter))
	model = updated.(Model)
	if cmd == nil || !model.actionInFlight {
		t.Fatal("Enter did not accept rename")
	}
	model = updateModel(t, model, cmd())
	if model.toast != "container renamed" {
		t.Fatalf("rename toast = %q", model.toast)
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
	model = updateModel(t, model, keyMsg(tea.KeyUp))
	groupFooter := shell.Plain(model.footer())
	if strings.Contains(groupFooter, "Enter") || strings.Contains(groupFooter, "Ctrl-R") {
		t.Fatalf("group footer advertises unavailable action: %q", groupFooter)
	}
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	model = updateModel(t, model, keyMsg(tea.KeyDown))
	projectFooter := shell.Plain(model.footer())
	if !strings.Contains(projectFooter, "Enter") || !strings.Contains(projectFooter, "fold") {
		t.Fatalf("project footer has wrong actions: %q", projectFooter)
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

var ansiParameters = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// backgroundCoverage walks a styled line and reports how many visible cells
// had an active background out of the line's total width.
func backgroundCoverage(line string) (covered, total int) {
	background := false
	for index := 0; index < len(line); {
		if line[index] == '\x1b' {
			match := ansiParameters.FindStringSubmatch(line[index:])
			if match == nil {
				index++
				continue
			}
			parameters := strings.FieldsFunc(match[1], func(r rune) bool { return r == ';' || r == ':' })
			if len(parameters) == 0 {
				background = false
			}
			for _, parameter := range parameters {
				value, _ := strconv.Atoi(parameter)
				if value == 0 || value == 49 {
					background = false
				}
				if value == 48 || value == 7 {
					background = true
				}
			}
			index += len(match[0])
			continue
		}
		_, size := utf8.DecodeRuneInString(line[index:])
		width := shell.StringWidth(line[index : index+size])
		total += width
		if background {
			covered += width
		}
		index += size
	}
	return covered, total
}

func channel(hex string) (int, int, int) {
	r, g, b, _ := lipgloss.Color(hex).RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}

// TestSelectedRowUsesLightForegroundAcrossTheFullWidth checks the selection
// is readable in every tab and in both themes: each painted row carries the
// explicit selection foreground and its background covers every column,
// padding included.
func TestSelectedRowUsesLightForegroundAcrossTheFullWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		style := lipgloss.NewStyle().Background(selectionBackground()).Foreground(selectionForeground())
		start := strings.TrimSuffix(style.Render(""), "\x1b[0m")
		if start == "" {
			t.Fatal("selection style rendered no escape sequence")
		}
		for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
			model := NewModel(context.Background(), richDeps(), tab)
			model.SetSize(100, 24)
			cursorable := []string{}
			for _, row := range model.rows {
				if row.cursorable {
					cursorable = append(cursorable, row.id)
				}
			}
			if len(cursorable) == 0 {
				t.Fatalf("tab %q has no cursorable rows", tab)
			}
			for _, id := range cursorable {
				model.cursor = id
				lines := strings.Split(model.View(), "\n")
				painted := 0
				for _, line := range lines[2 : len(lines)-1] {
					if !strings.HasPrefix(line, start) {
						continue
					}
					painted++
					covered, total := backgroundCoverage(line)
					if covered != total || total != model.width {
						t.Fatalf("dark=%v tab %q row %q: background covers %d of %d cells (width %d): %q", dark, tab, id, covered, total, model.width, line)
					}
				}
				if painted == 0 {
					t.Fatalf("dark=%v tab %q row %q has no selection background", dark, tab, id)
				}
			}
		}
		if dark {
			if r, g, b := channel(selectionForeground().Dark); r < 200 || g < 200 || b < 200 {
				t.Fatalf("dark selection foreground is not light: %d,%d,%d", r, g, b)
			}
		} else if r, g, b := channel(selectionForeground().Light); r > 80 || g > 80 || b > 80 {
			t.Fatalf("light selection foreground is not dark: %d,%d,%d", r, g, b)
		}
	}
}

// selectedRowPrefix is the escape sequence that opens a painted selection row:
// its adaptive background and foreground.
func selectedRowPrefix() string {
	style := lipgloss.NewStyle().Background(selectionBackground()).Foreground(selectionForeground())
	return strings.TrimSuffix(style.Render(""), "\x1b[0m")
}

// selectionForegroundToken renders the selection foreground and returns the SGR
// parameters that paint it, so tests can compare them to a row's active colour.
func selectionForegroundToken(t *testing.T) string {
	t.Helper()
	tokens := foregroundTokens(selectedRowPrefix() + "x")
	if len(tokens) == 0 || tokens[0] == "" {
		t.Fatalf("selection style rendered no foreground: %q", selectedRowPrefix())
	}
	return tokens[0]
}

// foregroundTokens returns the foreground active at every visible rune of a
// styled line. A token is the raw SGR parameter list ("31", "38;5;12",
// "38;2;r;g;b") or "" for the terminal default.
func foregroundTokens(value string) []string {
	tokens := []string{}
	current := ""
	for index := 0; index < len(value); {
		if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '[' {
			end := strings.IndexByte(value[index+2:], 'm')
			if end < 0 {
				break
			}
			end += index + 2
			params := strings.FieldsFunc(value[index+2:end], func(r rune) bool { return r == ';' || r == ':' })
			for position := 0; position < len(params); position++ {
				code, err := strconv.Atoi(params[position])
				if err != nil {
					continue
				}
				switch {
				case code == 0 || code == 39:
					current = ""
				case code >= 30 && code <= 37, code >= 90 && code <= 97:
					current = params[position]
				case code == 38 && position+1 < len(params):
					switch params[position+1] {
					case "5":
						if position+2 < len(params) {
							current = strings.Join(params[position:position+3], ";")
							position += 2
						}
					case "2":
						if position+4 < len(params) {
							current = strings.Join(params[position:position+5], ";")
							position += 4
						}
					}
				}
			}
			index = end + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(value[index:])
		tokens = append(tokens, current)
		index += size
	}
	return tokens
}

// selectionDeps builds one row of every kind that carries its own colour: a
// project with a full Git summary, a container to mark, and agents in each
// state. "con" matches all of them so the query paints them on the selection.
func selectionDeps() Deps {
	since := time.Now().Add(-2 * time.Minute)
	local := picker.LocalMachine()
	return Deps{
		Projects: []picker.Place{{
			Kind: picker.ProjectKind, Path: "/work/con", Group: "/work", DisplayRemainder: "con", Git: true,
			GitContext: &picker.GitContext{Identity: "con", Adds: 2, Deletes: 1, Dirty: true, Ahead: 1, Behind: 1},
		}},
		Containers: []capabilities.Container{{ID: "con-one", Name: "con-one", Machine: local}},
		Agents: []capabilities.Agent{
			{PaneID: "agent-one", Target: "con-one:0.1", ContainerID: "con-one", Container: "con-one", Harness: "con-pi", State: "stopped", Seen: true, StoppedSince: &since, Branch: "con-branch"},
			{PaneID: "agent-two", Target: "two:0.1", ContainerID: "two", Container: "two", Harness: "claude", State: "stopped", StoppedSince: &since},
			{PaneID: "agent-three", Target: "three:0.1", ContainerID: "three", Container: "three", Harness: "work", State: "working"},
		},
	}
}

func contrastRatioHex(foreground, background string) float64 {
	luminance := func(value string) float64 {
		value = strings.TrimPrefix(value, "#")
		channels := [3]float64{}
		for index := range channels {
			channel, err := strconv.ParseUint(value[index*2:index*2+2], 16, 8)
			if err != nil {
				panic(err)
			}
			linear := float64(channel) / 255
			if linear <= 0.04045 {
				channels[index] = linear / 12.92
			} else {
				channels[index] = math.Pow((linear+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
	}
	light, dark := luminance(foreground), luminance(background)
	if light < dark {
		light, dark = dark, light
	}
	return (light + 0.05) / (dark + 0.05)
}

func TestSelectedStateColoursContrastWithSelection(t *testing.T) {
	selection := selectionBackground()
	states := []capabilities.Agent{{State: "stopped"}, {State: "stopped", Seen: true}, {State: "working"}}
	for _, dark := range []bool{true, false} {
		background := selection.Dark
		if !dark {
			background = selection.Light
		}
		for _, agent := range states {
			foreground := selectedStateForeground(agent)
			value := foreground.Dark
			if !dark {
				value = foreground.Light
			}
			if ratio := contrastRatioHex(value, background); ratio < 4.5 {
				t.Fatalf("dark=%t state=%q contrast=%0.2f, want at least 4.5", dark, agent.State, ratio)
			}
		}
	}
}

func selectedStateCells(model Model, tab Tab, row row, plain string) map[int]bool {
	allowed := map[int]bool{}
	runes := []rune(plain)
	if tab == ContainersTab {
		for index, value := range runes {
			if value == '●' {
				allowed[index] = true
			}
		}
		return allowed
	}
	if tab != AgentsTab {
		return allowed
	}
	state := strings.ToLower(strings.TrimSpace(sanitize(row.agent.State)))
	label := "● " + state
	if state == "stopped" && row.agent.StoppedSince != nil {
		label += " " + stoppedAge(model.now(), *row.agent.StoppedSince)
	}
	labelRunes := []rune(label)
	for index := 0; index+len(labelRunes) <= len(runes); index++ {
		if string(runes[index:index+len(labelRunes)]) != label {
			continue
		}
		for offset := range labelRunes {
			allowed[index+offset] = true
		}
		break
	}
	return allowed
}

// TestSelectedRowForegrounds checks that selection text uses one foreground,
// while agent state cues keep their explicit adaptive colours. The fixture
// carries a search query, a marked item, a Git summary and every agent state,
// so each tab exercises the coloured segments that must remain readable.
func TestSelectedRowHasOnlySelectionForeground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		expected := selectionForegroundToken(t)
		stateTokens := map[string]bool{}
		for _, agent := range selectionDeps().Agents {
			for _, token := range foregroundTokens(selectedStateColor(agent).Render("x")) {
				if token != "" {
					stateTokens[token] = true
				}
			}
		}
		for _, tab := range []Tab{AgentsTab, ContainersTab, ProjectsTab} {
			model := NewModel(context.Background(), selectionDeps(), tab)
			model.SetSize(120, 30)
			model.SetQuery("con")
			if tab == ContainersTab {
				model.toggleMark()
			}
			cues := map[string]bool{}
			for _, row := range model.rows {
				if !row.cursorable {
					continue
				}
				model.cursor = row.id
				lines := strings.Split(model.View(), "\n")
				painted := 0
				for _, line := range lines[2 : len(lines)-1] {
					if !strings.HasPrefix(line, selectedRowPrefix()) {
						continue
					}
					painted++
					plain := shell.Plain(line)
					stateCells := selectedStateCells(model, tab, row, plain)
					for cell, token := range foregroundTokens(line) {
						if token != expected && !stateTokens[token] {
							t.Fatalf("dark=%v tab %q row %q cell %d: foreground %q, want selection or state: %q", dark, tab, row.id, cell, token, line)
						}
						if stateTokens[token] && !stateCells[cell] {
							t.Fatalf("dark=%v tab %q row %q cell %d: state foreground %q outside state cue: %q", dark, tab, row.id, cell, token, line)
						}
					}
					for _, cue := range []string{"+2", "−1", "*", "↑1", "↓1", "◆", "●"} {
						if strings.Contains(plain, cue) {
							cues[cue] = true
						}
					}
				}
				if painted == 0 {
					t.Fatalf("dark=%v tab %q row %q has no selected line", dark, tab, row.id)
				}
			}
			// Each tab must actually render its cue on the selection, otherwise
			// the foreground check above proves nothing.
			if cue := map[Tab]string{AgentsTab: "●", ContainersTab: "◆", ProjectsTab: "+2"}[tab]; !cues[cue] {
				t.Fatalf("dark=%v tab %q did not render cue %q on the selected rows", dark, tab, cue)
			}
		}
	}
}

// TestPaletteMatchesTheIcon pins every brand colour to assets/logo.svg.
func TestPaletteMatchesTheIcon(t *testing.T) {
	got := map[string]string{
		"ground":      palette.Ground.Dark,
		"tile":        palette.Tile.Dark,
		"tile-border": palette.TileBorder.Dark,
		"fg":          palette.Fg.Dark,
		"muted":       palette.Muted.Dark,
		"accent":      palette.Accent.Dark,
	}
	want := map[string]string{
		"ground":      "#0F1115",
		"tile":        "#171A21",
		"tile-border": "#2A2F3A",
		"fg":          "#E8EAED",
		"muted":       "#8A919E",
		"accent":      "#B8F15A",
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("%s dark = %q, want %q", name, got[name], value)
		}
	}
	// The colours that would be unreadable on a light terminal need their own
	// light variant: an olive accent, a darker muted grey and a near-black fg.
	for name, pair := range map[string]lipgloss.AdaptiveColor{"fg": palette.Fg, "muted": palette.Muted, "accent": palette.Accent} {
		if pair.Light == pair.Dark {
			t.Fatalf("%s light variant equals the dark variant %q", name, pair.Light)
		}
	}
}

// TestAccentRendersTheIconLimeInTrueColor pins the brand accent on a dark
// background: the highlight glyph and key hints use the icon lime instead of an
// ANSI colour.
func TestAccentRendersTheIconLimeInTrueColor(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	lipgloss.SetHasDarkBackground(true)
	tokens := foregroundTokens(accent().Render("x"))
	icon := foregroundTokens(lipgloss.NewStyle().Foreground(lipgloss.Color("#B8F15A")).Render("x"))
	if len(tokens) == 0 || len(icon) == 0 || tokens[0] != icon[0] {
		t.Fatalf("accent foreground = %v, want the icon lime #B8F15A = %v", tokens, icon)
	}
	model := NewModel(context.Background(), richDeps(), AgentsTab)
	model.SetSize(100, 24)
	if view := model.View(); !strings.Contains(view, "\x1b["+icon[0]+"m⌕ ") {
		t.Fatalf("search glyph did not use the icon lime: %q", view)
	}
}

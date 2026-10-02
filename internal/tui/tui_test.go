package tui

import (
	"context"
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

func updateModel(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	return updated.(Model)
}

func keyMsg(key tea.KeyType, runes ...rune) tea.KeyMsg {
	return tea.KeyMsg{Type: key, Runes: runes}
}

func testContainerDeps() Deps {
	return Deps{Containers: []capabilities.Container{
		{ID: "one", Name: "one", Machine: picker.LocalMachine()},
		{ID: "two", Name: "two", Machine: "remote"},
	}}
}

func TestNearestCursorableClampsRemovedRows(t *testing.T) {
	rows := []row{{id: "first", cursorable: true}, {id: "second", cursorable: true}, {id: "third", cursorable: true}}
	if got, ok := nearestCursorable(rows, 10); !ok || got != "third" {
		t.Fatalf("nearest row for removed bottom selection = %q, %t; want third", got, ok)
	}
}

func TestPrintableKeysAlwaysSearch(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	for _, value := range []rune{'d', 'x', 'r', 'q'} {
		model = updateModel(t, model, keyMsg(tea.KeyRunes, value))
	}
	if got, want := model.Query(), "dxrq"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	if model.Cancelled() {
		t.Fatal("printable input closed the model")
	}
}

func TestQuerySelectsProjectNotGroupHeader(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo", Git: true},
		{Kind: picker.ProjectKind, Path: "/work/other", Group: "/work", DisplayRemainder: "other", Git: true},
	}
	model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
	model.SetQuery("nexo")
	if !strings.HasPrefix(model.Cursor(), "project:") {
		t.Fatalf("cursor = %q, want a project identity", model.Cursor())
	}
	if model.Cursor() == "group:/work" {
		t.Fatal("query selected the group header")
	}
}

func TestMachineGroupsDoNotFold(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyUp))
	before := append([]string(nil), model.Rows()...)
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	if got := model.Rows(); strings.Join(got, "\x00") != strings.Join(before, "\x00") {
		t.Fatalf("right arrow changed machine group rows: %v -> %v", before, got)
	}
}

func TestCtrlTMarksContainer(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyCtrlT))
	if got := model.Marked(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("marked = %v, want [one]", got)
	}
}

func TestMarkedNameKeepsAccentForegroundWhenUnselected(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	lipgloss.SetHasDarkBackground(true)
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyCtrlT))
	model.SetQuery("one")
	model.SetSize(80, 24)
	// Clear the selection: a selected row drops every per-segment colour and
	// keeps the mark as bold weight instead.
	model.input.cursor = ""
	var line string
	for _, candidate := range strings.Split(model.View(), "\n") {
		if strings.Contains(shell.Plain(candidate), "◆ one") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatal("marked container line was not rendered")
	}
	runes := []rune(shell.Plain(line))
	tokens := foregroundTokens(line)
	if len(tokens) != len(runes) {
		t.Fatalf("parsed %d foreground tokens for %d runes: %q", len(tokens), len(runes), line)
	}
	expected := foregroundTokens(accent().Render("x"))
	if len(expected) == 0 || expected[0] == "" {
		t.Fatal("accent rendered no foreground")
	}
	for index := 0; index+2 < len(runes); index++ {
		if string(runes[index:index+3]) != "one" {
			continue
		}
		for _, token := range tokens[index : index+3] {
			if token != expected[0] {
				t.Fatalf("marked name has foreground %q, want the accent %q: %q", token, expected[0], line)
			}
		}
		return
	}
	t.Fatalf("marked name not found in styled line: %q", line)
}

func backgroundCells(value string) []bool {
	background := false
	cells := []bool{}
	for index := 0; index < len(value); {
		if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '[' {
			end := strings.IndexByte(value[index+2:], 'm')
			if end < 0 {
				break
			}
			end += index + 2
			params := value[index+2 : end]
			codes := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
			for position := 0; position < len(codes); position++ {
				code, err := strconv.Atoi(codes[position])
				if err != nil {
					continue
				}
				switch code {
				case 0:
					background = false
				case 48:
					background = true
					if position+1 < len(codes) {
						switch codes[position+1] {
						case "5":
							position += 2
						case "2":
							position += 4
						}
					}
				case 49:
					background = false
				}
			}
			index = end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		for width := shell.StringWidth(string(r)); width > 0; width-- {
			cells = append(cells, background)
		}
		index += size
	}
	return cells
}

func assertSelectedBackgroundCells(t *testing.T, model Model, selectedID string) {
	t.Helper()
	frame := strings.Split(model.View(), "\n")
	bodyHeight := model.height - 3
	lines, start := model.frame().bodyLayout(bodyHeight)
	for screenLine := 0; screenLine < bodyHeight; screenLine++ {
		line := frame[2+screenLine]
		cells := backgroundCells(line)
		if len(cells) != model.width {
			t.Fatalf("body line %d has %d parsed cells, want %d: %q", screenLine, len(cells), model.width, line)
		}
		lineIndex := start + screenLine
		wantBackground := lineIndex < len(lines) && lines[lineIndex].rowID == selectedID
		for cell, gotBackground := range cells {
			if gotBackground != wantBackground {
				t.Fatalf("line %d cell %d background = %t, want %t (selected=%q): %q", screenLine, cell, gotBackground, wantBackground, selectedID, line)
			}
		}
	}
}

func TestSelectedRowBackgroundCoversEveryCell(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	project := picker.Place{
		Kind: picker.ProjectKind, Path: "/work/東京😀/root", Group: "/work/東京😀", DisplayRemainder: "東京😀-root", Git: true,
		GitContext: &picker.GitContext{Identity: "feature", Adds: 2, Deletes: 1, Dirty: true, Ahead: 1, Behind: 1},
	}
	cases := []struct {
		name     string
		model    Model
		selected string
		query    string
	}{
		{
			name:     "folder header",
			model:    NewModel(context.Background(), Deps{Projects: []picker.Place{project}}, ProjectsTab),
			selected: "group:/work/東京😀",
		},
		{
			name:     "matched git project",
			model:    NewModel(context.Background(), Deps{Projects: []picker.Place{project}}, ProjectsTab),
			selected: "project:/work/東京😀/root",
			query:    "東京",
		},
		{
			name: "matched pill container",
			model: NewModel(context.Background(), Deps{
				Containers: []capabilities.Container{{ID: "one", Name: "東京😀", Machine: picker.LocalMachine()}},
				Agents:     []capabilities.Agent{{ContainerID: "one", Container: "東京😀", Harness: "pi", Target: "one:0.1", State: "working"}},
			}, ContainersTab),
			selected: "container:" + picker.LocalMachine() + ":one",
			query:    "東京",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := tc.model
			model.SetSize(80, 10)
			if tc.query != "" {
				model.SetQuery(tc.query)
			}
			model.input.cursor = tc.selected
			if model.Cursor() != tc.selected {
				t.Fatalf("cursor = %q, want %q; rows=%v", model.Cursor(), tc.selected, model.Rows())
			}
			assertSelectedBackgroundCells(t, model, tc.selected)
		})
	}
}

func TestSelectedRowHasTheOnlyBackgroundSGR(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model.SetSize(80, 24)
	frame := model.View()
	lines := strings.Split(frame, "\n")
	count := 0
	for lineNumber, line := range lines {
		matches := backgroundSGRs(line)
		if len(matches) > 0 {
			if lineNumber < 2 || lineNumber == len(lines)-1 {
				t.Fatalf("background painted outside the body: %q", frame)
			}
			count += len(matches)
		}
	}
	if count == 0 {
		t.Fatalf("selected row has no background: %q", frame)
	}
}

func TestStateColorMapsEachState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent capabilities.Agent
		want  lipgloss.TerminalColor
	}{
		{"unseen stopped", capabilities.Agent{State: "stopped"}, lipgloss.Color("1")},
		{"working", capabilities.Agent{State: "working"}, lipgloss.Color("3")},
		{"seen stopped", capabilities.Agent{State: "stopped", Seen: true}, palette.Muted},
	} {
		if got := stateColor(tc.agent).GetForeground(); got != tc.want {
			t.Errorf("%s: stateColor foreground = %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestSelectedStateColoursMatchUnselected(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	since := time.Now().Add(-2 * time.Hour)
	deps := Deps{Containers: []capabilities.Container{{ID: "one", Name: "one", Machine: picker.LocalMachine()}}, Agents: []capabilities.Agent{
		{PaneID: "red", ContainerID: "one", Container: "one", Harness: "pi", State: "stopped", StoppedSince: &since},
		{PaneID: "grey", ContainerID: "one", Container: "one", Harness: "pi", State: "stopped", Seen: true, StoppedSince: &since},
		{PaneID: "amber", ContainerID: "one", Container: "one", Harness: "pi", State: "working"},
	}}
	for _, dark := range []bool{true, false} {
		lipgloss.SetHasDarkBackground(dark)
		for _, tab := range []Tab{AgentsTab, ContainersTab} {
			model := NewModel(context.Background(), deps, tab)
			model.SetSize(100, 20)
			for _, row := range model.rows {
				if row.kind != itemRow {
					continue
				}
				model.input.cursor = row.id
				line := ""
				for _, candidate := range strings.Split(model.View(), "\n") {
					if strings.Contains(shell.Plain(candidate), "●") && strings.HasPrefix(candidate, selectedRowPrefix()) {
						line = candidate
						break
					}
				}
				if line == "" {
					t.Fatalf("dark=%v tab=%q row=%q has no selected state line", dark, tab, row.id)
				}
				if tab == AgentsTab {
					token := foregroundTokens(stateColor(row.agent).Render("x"))[0]
					if !strings.Contains(line, "\x1b["+token+"m") {
						t.Fatalf("dark=%v tab=%q row=%q state colour %q differs from the unselected row: %q", dark, tab, row.id, token, line)
					}
				} else {
					for _, agent := range deps.Agents {
						token := foregroundTokens(stateColor(agent).Render("x"))[0]
						if !strings.Contains(line, "\x1b["+token+"m") {
							t.Fatalf("dark=%v tab=%q row=%q dot colour %q differs from the unselected row: %q", dark, tab, row.id, token, line)
						}
					}
				}
			}
		}
	}
}

func TestSelectedRowPreservesPlainTextAndWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	deps := testContainerDeps()
	deps.Containers[0].Name = "東京😀"
	model := NewModel(context.Background(), deps, ContainersTab)
	model.SetSize(24, 8)
	rows := strings.Split(model.View(), "\n")
	var selected string
	for _, line := range rows[2 : len(rows)-1] {
		if strings.Contains(shell.Plain(line), "東京😀") {
			selected = line
			break
		}
	}
	if selected == "" {
		t.Fatalf("selected CJK/emoji row was not rendered; cursor=%q rows=%v view=%q", model.Cursor(), model.Rows(), model.View())
	}
	if got := shell.StringWidth(selected); got != 24 {
		t.Fatalf("selected row width = %d, want 24: %q", got, selected)
	}
	plain := shell.Plain(selected)
	if !strings.Contains(plain, "東京😀") {
		t.Fatalf("selected row changed plain text: %q", plain)
	}
	if !strings.HasPrefix(selected, "\x1b[") || !strings.HasSuffix(selected, "\x1b[0m") {
		t.Fatalf("selected row background does not cover its full line: %q", selected)
	}
	for _, line := range rows[2 : len(rows)-1] {
		if !strings.Contains(shell.Plain(line), "東京😀") && strings.HasPrefix(line, "\x1b[") && strings.Contains(line, "48;") {
			t.Fatalf("selection background bled to another row: %q", line)
		}
	}
}

func TestRenderFillsRequestedSurface(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	for _, size := range [][2]int{{200, 50}, {100, 30}, {80, 24}, {60, 16}, {40, 6}, {80, 4}} {
		model.SetSize(size[0], size[1])
		frame := model.View()
		lines := strings.Split(frame, "\n")
		if len(lines) != size[1] {
			t.Fatalf("size %dx%d rendered %d lines", size[0], size[1], len(lines))
		}
		for line, value := range lines {
			if width := shell.StringWidth(value); width != size[0] {
				t.Fatalf("size %dx%d line %d has width %d: %q", size[0], size[1], line, width, value)
			}
		}
	}
}

// BenchmarkContainerRowsMultiMachine exercises the row rebuild with local and
// remote containers where every row already carries its associated agents.
func BenchmarkContainerRowsMultiMachine(b *testing.B) {
	machines := []string{"dev", "stage", "prod"}
	containers := make([]capabilities.Container, 0, 30)
	for index := 0; index < 30; index++ {
		id := "local-" + strconv.Itoa(index)
		containers = append(containers, capabilities.Container{ID: id, Name: id, Machine: picker.LocalMachine()})
	}
	model := NewModel(context.Background(), Deps{Containers: containers, RemoteMachines: machines}, ContainersTab)
	for _, machine := range machines {
		places := make([]picker.Place, 0, 30)
		for index := 0; index < 30; index++ {
			id := machine + "-" + strconv.Itoa(index)
			places = append(places, picker.Place{Kind: picker.ContainerKind, ContainerID: id, ContainerName: id})
		}
		model.source.applyRemote(picker.RemoteBatch{Machine: machine, Places: places})
		model.rebuild(false)
	}
	model.SetSize(120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		model.projection().Rows(ContainersTab)
	}
}

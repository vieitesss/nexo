package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func rowByID(model Model, id string) (row, bool) {
	for _, candidate := range model.rows {
		if candidate.id == id {
			return candidate, true
		}
	}
	return row{}, false
}

func TestSearchWordDeletes(t *testing.T) {
	cases := []struct {
		name  string
		query string
		key   tea.KeyMsg
		want  string
	}{
		{name: "ctrl w stops at whitespace", query: "hello/world again", key: tea.KeyMsg{Type: tea.KeyCtrlW}, want: "hello/world "},
		{name: "ctrl w treats punctuation as part of a word", query: "hello/world", key: tea.KeyMsg{Type: tea.KeyCtrlW}, want: ""},
		{name: "alt backspace stops at punctuation", query: "hello/world", key: tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}, want: "hello/"},
		{name: "alt backspace handles unicode", query: "δοκιμή/日本語", key: tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}, want: "δοκιμή/"},
		{name: "alt backspace handles punctuation runs", query: "one/two-three", key: tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}, want: "one/two-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
			model.SetQuery(tc.query)
			model = updateModel(t, model, tc.key)
			if got := model.Query(); got != tc.want {
				t.Fatalf("query = %q, want %q", got, tc.want)
			}
		})
	}

	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model.SetQuery("one two")
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlU})
	if model.Query() != "" {
		t.Fatalf("ctrl-u query = %q, want empty", model.Query())
	}
}

func TestMouseIgnoresActionInFlight(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model.SetSize(80, 8)
	before := model.Cursor()
	model.actionInFlight = true
	model = updateModel(t, model, tea.MouseMsg{X: 8, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Cursor() != before {
		t.Fatalf("cursor changed during action: %q -> %q", before, model.Cursor())
	}
	if _, ok := model.SelectedPlace(); ok {
		t.Fatal("mouse accepted a row while an action was in flight")
	}
}

func TestMouseIgnoresModalClicksAndWheel(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/root", Group: "/work", DisplayRemainder: "root"},
		{Kind: picker.ProjectKind, Path: "/work/root/wt", Group: "/work", DisplayRemainder: "wt", ParentPath: "/work/root"},
	}
	for _, mode := range []string{"kill", "rename"} {
		t.Run(mode, func(t *testing.T) {
			model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
			model.SetSize(80, 8)
			beforeCursor, beforeRows := model.Cursor(), append([]string(nil), model.Rows()...)
			if mode == "kill" {
				model.confirmKill = true
			} else {
				model.renameMode = true
			}
			model = updateModel(t, model, tea.MouseMsg{X: 8, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			model = updateModel(t, model, tea.MouseMsg{X: 8, Y: 3, Button: tea.MouseButtonWheelDown})
			if model.Cursor() != beforeCursor {
				t.Fatalf("cursor changed during %s modal: %q -> %q", mode, beforeCursor, model.Cursor())
			}
			if got := model.Rows(); strings.Join(got, "\x00") != strings.Join(beforeRows, "\x00") {
				t.Fatalf("rows changed during %s modal: %v -> %v", mode, beforeRows, got)
			}
			if mode == "kill" && !model.confirmKill {
				t.Fatal("kill confirmation ended from a mouse event")
			}
			if mode == "rename" && !model.renameMode {
				t.Fatal("rename mode ended from a mouse event")
			}
		})
	}
}

func TestMouseCheckoutChevronAndOpenHitAreas(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/root", Group: "/work", DisplayRemainder: "root"},
		{Kind: picker.ProjectKind, Path: "/work/root/wt", Group: "/work", DisplayRemainder: "wt", ParentPath: "/work/root"},
	}
	model := NewModel(context.Background(), Deps{Projects: projects}, ProjectsTab)
	model.SetSize(80, 8)
	if got := model.Rows(); len(got) != 1 {
		t.Fatalf("initial rows = %v, want folded group", got)
	}
	// Folders start folded; expand the folder before testing its checkout row.
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	if got := model.Rows(); len(got) != 2 {
		t.Fatalf("rows after folder expansion = %v, want group and folded checkout", got)
	}

	// The root checkout chevron is rendered after the two-cell row marker.
	model = updateModel(t, model, tea.MouseMsg{X: 2, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("rows after chevron click = %v, want expanded checkout", got)
	}
	// Clicking its name selects it without changing its fold state.
	model = updateModel(t, model, tea.MouseMsg{X: 8, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Cursor() != "project:/work/root" {
		t.Fatalf("cursor after checkout name click = %q", model.Cursor())
	}
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("rows after checkout name click = %v, want expanded checkout", got)
	}
	// A second click opens the checkout like Enter.
	model = updateModel(t, model, tea.MouseMsg{X: 8, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if place, ok := model.SelectedPlace(); !ok || place.Path != "/work/root" {
		t.Fatalf("second checkout click selected %+v, want root checkout", place)
	}
}

func TestMouseUsesRenderedBodyLayout(t *testing.T) {
	projects := []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/root", Group: "/work", DisplayRemainder: "root"},
		{Kind: picker.ProjectKind, Path: "/work/root/😀-子", Group: "/work", DisplayRemainder: "😀-子", ParentPath: "/work/root"},
		{Kind: picker.ProjectKind, Path: "/work/other", Group: "/work", DisplayRemainder: "other"},
	}
	model := NewModel(context.Background(), Deps{
		Projects:   projects,
		Containers: []capabilities.Container{{ID: "one", Name: "one", Machine: picker.LocalMachine()}},
	}, ContainersTab)
	model.SetSize(50, 6)

	// The tab hit area is the same text shown in the tab line.
	tabLine := shell.Plain(strings.Split(model.View(), "\n")[0])
	projectsX := strings.Index(tabLine, "Projects")
	if projectsX < 0 {
		t.Fatal("Projects tab was not rendered")
	}
	model = updateModel(t, model, tea.MouseMsg{X: projectsX, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Tab() != ProjectsTab {
		t.Fatalf("tab after click = %q, want projects", model.Tab())
	}

	// The first body row is a folded group header. Its click unfolds the group.
	if got := model.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("initial rows = %v, want folded group", got)
	}
	model = updateModel(t, model, tea.MouseMsg{X: 1, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := model.Rows(); len(got) != 3 {
		t.Fatalf("rows after folded header click = %v, want expanded group", got)
	}
	// Clicking it again folds it, rather than opening a header.
	model = updateModel(t, model, tea.MouseMsg{X: 1, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := model.Rows(); len(got) != 1 || got[0] != "group:/work" {
		t.Fatalf("rows after second header click = %v, want folded group", got)
	}
	// Expand it again, then expand the selected checkout, so the rendered body
	// has enough rows for a scrolled viewport.
	model = updateModel(t, model, tea.MouseMsg{X: 1, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	model = updateModel(t, model, keyMsg(tea.KeyDown))
	model = updateModel(t, model, keyMsg(tea.KeyRight))
	if got := model.Rows(); len(got) != 4 {
		t.Fatalf("rows after checkout expansion = %v, want expanded group and checkout", got)
	}

	// The rendered body has a scrolled viewport. Click a visible child using
	// its screen coordinate and ensure it maps to the child, not its source
	// index before scrolling.
	for range 3 {
		model = updateModel(t, model, tea.MouseMsg{X: 1, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	}
	_, start := model.bodyLayout(model.height - 3)
	if start == 0 {
		t.Fatal("wheel did not scroll the rendered body")
	}
	lines, _ := model.bodyLayout(model.height - 3)
	visibleIndex := start
	for index := start; index < len(lines); index++ {
		if lines[index].rowID == "" {
			continue
		}
		candidate, found := rowByID(model, lines[index].rowID)
		if found && !candidate.foldable {
			visibleIndex = index
			break
		}
	}
	visible := lines[visibleIndex]
	if visible.rowID == "" {
		t.Fatalf("scrolled line has no row identity: %+v", visible)
	}
	clickY := 2 + visibleIndex - start
	model = updateModel(t, model, tea.MouseMsg{X: 4, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Cursor() != visible.rowID {
		t.Fatalf("cursor after scrolled click = %q, want %q", model.Cursor(), visible.rowID)
	}

	// The next click on an already-selected item opens it like Enter.
	model = updateModel(t, model, tea.MouseMsg{X: 4, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if _, ok := model.SelectedPlace(); !ok {
		t.Fatal("second click did not open the selected project")
	}
}

func TestMouseSelectsFlatSearchRow(t *testing.T) {
	model := NewModel(context.Background(), Deps{Projects: projectFixture()}, ProjectsTab)
	model.SetQuery("gitops")
	model.SetSize(120, 12)
	lines, start := model.bodyLayout(model.height - 3)
	visibleIndex := -1
	for index := start; index < len(lines); index++ {
		if lines[index].rowID != "" && lines[index].rowID != model.Cursor() {
			visibleIndex = index
			break
		}
	}
	if visibleIndex < 0 {
		t.Fatal("flat search did not render a second row")
	}
	clicked := lines[visibleIndex].rowID
	clickY := 2 + visibleIndex - start
	model = updateModel(t, model, tea.MouseMsg{X: 8, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Cursor() != clicked {
		t.Fatalf("cursor after flat row click = %q, want %q", model.Cursor(), clicked)
	}
	model = updateModel(t, model, tea.MouseMsg{X: 8, Y: clickY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	place, ok := model.SelectedPlace()
	if !ok || place.Path == "" {
		t.Fatalf("second flat row click selected %+v, want a project", place)
	}
}

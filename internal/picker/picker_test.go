package picker

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/projects"
)

func testPlaces() []Place {
	return []Place{
		{Kind: ProjectKind, Group: "/work/alpha", Path: "/work/alpha/app"},
		{Kind: ProjectKind, Group: "/work/alpha", Path: "/work/alpha/notes"},
		{Kind: ProjectKind, Group: "/work/beta", Path: "/work/beta/gamma"},
	}
}

func TestProjectPlacesCarriesGitContextAndOpenFields(t *testing.T) {
	places := ProjectPlaces([]projects.Candidate{{Path: "/work/app", Group: "/work", Git: true, LinkedWorktree: true}})
	if len(places) != 1 {
		t.Fatalf("places = %#v", places)
	}
	place := places[0]
	if !place.Git || !place.LinkedWorktree || place.SearchText != "/work/app" || place.DisplayRemainder != "app" || place.Payload == nil {
		t.Fatalf("place = %#v, want project metadata and open payload", place)
	}
}

func TestEmptyQueryFoldsGroupsInFirstSeenOrder(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	got := model.View()
	want := "> \n▸ alpha (2)\n▸ beta (1)"
	if got != want {
		t.Fatalf("View() = %q, want %q", got, want)
	}
	if len(model.Rows()) != 2 || model.Rows()[0].Kind != HeaderRow || model.Rows()[1].Group != "/work/beta" {
		t.Fatalf("rows = %#v, want folded first-seen groups", model.Rows())
	}
}

func TestHeaderExpandAndChildCollapseAreKeyboardStops(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if len(model.Rows()) != 4 || model.Rows()[1].Kind != PlaceRow {
		t.Fatalf("expanded rows = %#v", model.Rows())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	if len(model.Rows()) != 2 || model.Rows()[0].Expanded || model.Cursor() != 0 {
		t.Fatalf("collapsed rows = %#v cursor=%d, want parent header selected", model.Rows(), model.Cursor())
	}
}

func TestSpaceExpandsHeader(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	if len(model.Rows()) != 4 || model.Rows()[0].Kind != HeaderRow || !model.Rows()[0].Expanded {
		t.Fatalf("rows = %#v, want Space to expand header", model.Rows())
	}
}

func TestEnterOnlyExpandsAnExpandedHeader(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	before := model.Rows()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil || len(model.Rows()) != len(before) || !model.Rows()[0].Expanded {
		t.Fatalf("rows = %#v, cmd=%v, Enter collapsed expanded header", model.Rows(), cmd)
	}
}

func TestRemainderHitUnfoldsOnlyMatchingGroupAndRanksChildren(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	model.SetQuery("am")
	rows := model.Rows()
	if len(rows) != 2 || rows[0].Kind != HeaderRow || rows[0].Group != "/work/beta" || rows[1].Place.Path != "/work/beta/gamma" {
		t.Fatalf("rows = %#v, want beta remainder hit", rows)
	}
	if strings.Contains(model.View(), "/work") {
		t.Fatalf("view = %q, collapsed prefix should not be rendered", model.View())
	}
}

func TestPrefixOnlyHitLeavesGroupFolded(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	model.SetQuery("/work")
	rows := model.Rows()
	if len(rows) != 2 || rows[0].Kind != HeaderRow || rows[0].Expanded || rows[1].Kind != HeaderRow || rows[1].Expanded {
		t.Fatalf("rows = %#v, want folded prefix-only groups", rows)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.Rows()) <= 2 || !model.Rows()[0].Expanded {
		t.Fatalf("rows = %#v, want Enter to expand a folded header", model.Rows())
	}
}

func TestClearingQueryRefoldsAllGroups(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	model.SetQuery("app")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.Query() != "" || len(model.Rows()) != 2 || model.Rows()[0].Expanded {
		t.Fatalf("rows = %#v query=%q, want all groups refolded", model.Rows(), model.Query())
	}
}

func TestEnterAcceptsPlaceButNotHeader(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("header Enter returned quit command")
	}
	if _, ok := model.Selected(); ok {
		t.Fatal("header Enter selected a place")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter on a child did not quit")
	}
	if selected, ok := model.Selected(); !ok || selected.Path != "/work/alpha/app" {
		t.Fatalf("selected = %#v, ok=%v", selected, ok)
	}
}

func TestThemeQueryTimesOutOnSilentTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := ThemeFromTerminal(ctx, ThemeQuery{Reader: blockingReader{}, Writer: &strings.Builder{}}, 2)
	if got.Foreground != DefaultTheme.Foreground || got.Background != DefaultTheme.Background || !got.Color {
		t.Fatalf("theme = %#v, want ANSI fallback", got)
	}
}

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

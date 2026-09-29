package picker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
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
	for range 3 {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		model = updated.(Model)
	}
	if model.Query() != "" || len(model.Rows()) != 2 || model.Rows()[0].Expanded {
		t.Fatalf("rows = %#v query=%q, want all groups refolded", model.Rows(), model.Query())
	}
}

func TestCancelKeysQuitWithoutSelecting(t *testing.T) {
	for _, keyType := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEsc} {
		model := NewModel(testPlaces(), Theme{})
		updated, cmd := model.Update(tea.KeyMsg{Type: keyType})
		model = updated.(Model)
		if cmd == nil || !model.Cancelled() {
			t.Fatalf("key %v: cancelled=%v cmd=%v, want clean quit", keyType, model.Cancelled(), cmd)
		}
	}
	model := NewModel(testPlaces(), Theme{})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	model = updated.(Model)
	if cmd == nil || !model.Cancelled() {
		t.Fatalf("q: cancelled=%v cmd=%v, want clean quit", model.Cancelled(), cmd)
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

func TestContainerPlacesFoldByMachineAndRenderCreateRowAndPreview(t *testing.T) {
	places := ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "dev", Machine: "laptop", Preview: "shell$"}})
	model := NewModel(places, Theme{})
	if got, want := model.View(), "> \n▸ laptop (1)"; got != want {
		t.Fatalf("folded view = %q, want %q", got, want)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	rows := model.Rows()
	if len(rows) != 3 || rows[1].Place.ContainerID != "$1" || rows[2].Kind != NewContainerRow {
		t.Fatalf("rows = %#v, want machine child and create row", rows)
	}
	if got := model.View(); !strings.Contains(got, "dev  shell$") || !strings.Contains(got, "+ new container…") {
		t.Fatalf("view = %q, want preview and create row", got)
	}
}

func TestPreviewBatchUpdatesContainerWithoutRebuildingPicker(t *testing.T) {
	model := NewModel(ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "one", Machine: "host"}}), Theme{})
	updated, _ := model.Update(PreviewBatch{Previews: map[ContainerRef]string{{Machine: "host", ID: "$1"}: "shell$"}})
	model = updated.(Model)
	places := model.Places()
	if len(places) == 0 || places[0].Preview != "shell$" {
		t.Fatalf("places = %#v, want asynchronously loaded preview", places)
	}
}

func TestContainerMarksAndKillConfirmOnceForSelection(t *testing.T) {
	places := ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "one", Machine: "host"}, {ID: "$2", Name: "two", Machine: "host"}})
	model := NewModel(places, Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if got, want := model.Marked(), []string{"$1", "$2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("marked = %#v, want %#v", got, want)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = updated.(Model)
	if _, ok := model.KillRequest(); ok {
		t.Fatal("kill request available before confirmation")
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("confirmation did not quit")
	}
	if got, ok := model.KillRequest(); !ok || !reflect.DeepEqual(got, []string{"$1", "$2"}) {
		t.Fatalf("kill request = %#v, %v", got, ok)
	}
}

func TestContainerRenameRequest(t *testing.T) {
	model := NewModel(ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "one", Machine: "host"}}), Theme{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("renamed")})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("rename did not quit")
	}
	if id, name, ok := model.RenameRequest(); !ok || id != "$1" || name != "renamed" {
		t.Fatalf("rename request = %q, %q, %v", id, name, ok)
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

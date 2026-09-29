package picker

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRepeatedHeadersSeparateConsecutiveRuns(t *testing.T) {
	places := []Place{
		{Kind: ProjectKind, Group: "/g1", Path: "/g1/p-one"},
		{Kind: ProjectKind, Group: "/g2", Path: "/g2/p-one"},
		{Kind: ProjectKind, Group: "/g1", Path: "/g1/p-two"},
	}
	model := NewModel(places, Theme{})
	model.SetQuery("p")
	rows := model.Rows()
	if len(rows) != 6 || rows[0].Group != "/g1" || rows[2].Group != "/g2" || rows[4].Group != "/g1" {
		t.Fatalf("rows = %#v, want repeated g1 headers", rows)
	}
	for range 5 {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	if model.Cursor() != 3 || model.Rows()[model.Cursor()].Group != "/g1" {
		t.Fatalf("cursor=%d rows=%#v, want nearest repeated header", model.Cursor(), model.Rows())
	}
}

func TestNoMatchGroupsAreHidden(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	model.SetQuery("does-not-exist")
	if rows := model.Rows(); len(rows) != 0 {
		t.Fatalf("rows = %#v, want no-match groups hidden", rows)
	}
}

func TestGoldenViewEmptyQuery(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	want := "> \n▸ alpha (2)\n▸ beta (1)"
	if got := model.View(); got != want {
		t.Fatalf("empty-query view = %q, want %q", got, want)
	}
}

func TestGoldenViewRemainderHit(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	model.SetQuery("am")
	want := "> am\n▾ beta\n  └ gamma"
	if got := model.View(); got != want {
		t.Fatalf("remainder-hit view = %q, want %q", got, want)
	}
}

func TestGoldenViewPrefixOnlyGroup(t *testing.T) {
	model := NewModel(testPlaces(), Theme{})
	model.SetQuery("/work")
	want := "> /work\n▸ alpha (2)\n▸ beta (1)"
	if got := model.View(); got != want {
		t.Fatalf("prefix-only view = %q, want %q", got, want)
	}
}

func TestPrefixOnlyChildrenRemainWhenAnotherChildUnfoldsGroup(t *testing.T) {
	places := []Place{
		{Kind: ProjectKind, Group: "/work/alpha", Path: "/work/alpha/app"},
		{Kind: ProjectKind, Group: "/work/alpha", Path: "/work/alpha/alpha-tool"},
	}
	model := NewModel(places, Theme{})
	model.SetQuery("alpha")
	rows := model.Rows()
	if len(rows) != 3 || rows[1].Place.Path != "/work/alpha/app" || rows[2].Place.Path != "/work/alpha/alpha-tool" {
		t.Fatalf("rows = %#v, want every full-path hit in unfolded group", rows)
	}
	if len(rows[1].Matches) != 0 {
		t.Fatalf("prefix-only child matches = %v, want no visible-remainder highlight", rows[1].Matches)
	}
}

func TestTreeSpineAndHighlightHaveSeparateThemeStyles(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	model := NewModel([]Place{{Kind: ProjectKind, Group: "/work", Path: "/work/nested/app", DisplayRemainder: "nested/app"}}, Theme{
		Foreground:          "31",
		Background:          "40",
		SelectionBackground: "44",
		Muted:               "90",
		Highlight:           "93",
		Color:               true,
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	model.SetQuery("app")
	view := model.View()
	if !strings.Contains(view, "│") {
		t.Fatalf("view = %q, want muted continuation spine", view)
	}
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("view = %q, want ANSI styling", view)
	}
	if !strings.Contains(view, "\x1b[1;") {
		t.Fatalf("view = %q, want bold highlight styling", view)
	}
}

func TestNamedOSCColorsBecomeANSIColors(t *testing.T) {
	responses := deadlineReader{Reader: strings.NewReader("\x1b]10;red\x07\x1b]11;blue\x07")}
	got := ThemeFromTerminal(context.Background(), ThemeQuery{Reader: responses, Writer: &strings.Builder{}}, time.Second)
	if got.Foreground != "1" || got.Background != "4" {
		t.Fatalf("theme = %#v, want ANSI foreground/background", got)
	}
	if got.SelectionBackground == "4" || got.Muted == "1" || !strings.HasPrefix(got.SelectionBackground, "#") || !strings.HasPrefix(got.Muted, "#") {
		t.Fatalf("theme = %#v, want blended ANSI colors", got)
	}
}

package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestRenderHasNoBackgroundSGR(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model.SetSize(80, 24)
	frame := model.View()
	if regexp.MustCompile(`\x1b\[[0-9;:]*([4][0-7]|10[0-7]|48)[^m]*m`).MatchString(frame) {
		t.Fatalf("frame contains a background SGR: %q", frame)
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

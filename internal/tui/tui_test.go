package tui

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestMarkedSearchNameKeepsMagentaForeground(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyCtrlT))
	model.SetQuery("one")
	model.SetSize(80, 24)
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
	styled := styledRunes(line)
	for index := 0; index+2 < len(styled); index++ {
		if styled[index].r == 'o' && styled[index+1].r == 'n' && styled[index+2].r == 'e' {
			for _, value := range styled[index : index+3] {
				if value.fg != 5 {
					t.Fatalf("name rune %q has foreground %d, want magenta (5): %q", value.r, value.fg, line)
				}
			}
			return
		}
	}
	t.Fatalf("marked name not found in styled line: %q", line)
}

type styledRune struct {
	r  rune
	fg int
}

func styledRunes(value string) []styledRune {
	foreground := -1
	result := []styledRune{}
	for index := 0; index < len(value); {
		if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '[' {
			end := strings.IndexByte(value[index+2:], 'm')
			if end < 0 {
				break
			}
			end += index + 2
			params := value[index+2 : end]
			codes := strings.Split(params, ";")
			for position := 0; position < len(codes); position++ {
				code, err := strconv.Atoi(codes[position])
				if err != nil {
					continue
				}
				switch {
				case code == 0:
					foreground = -1
				case code == 39:
					foreground = -1
				case code >= 30 && code <= 37:
					foreground = code - 30
				case code >= 90 && code <= 97:
					foreground = code - 90 + 8
				case code == 38 && position+2 < len(codes) && codes[position+1] == "5":
					if color, err := strconv.Atoi(codes[position+2]); err == nil {
						foreground = color
					}
					position += 2
				}
			}
			index = end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		result = append(result, styledRune{r: r, fg: foreground})
		index += size
	}
	return result
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

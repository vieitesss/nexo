package tui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

// boldCells returns whether the bold attribute is active at every visible cell
// of a styled line.
func boldCells(value string) []bool {
	bold := false
	cells := []bool{}
	for index := 0; index < len(value); {
		if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '[' {
			end := strings.IndexByte(value[index+2:], 'm')
			if end < 0 {
				break
			}
			end += index + 2
			for _, parameter := range strings.FieldsFunc(value[index+2:end], func(r rune) bool { return r == ';' || r == ':' }) {
				code, err := strconv.Atoi(parameter)
				if err != nil {
					continue
				}
				switch code {
				case 0, 22:
					bold = false
				case 1:
					bold = true
				}
			}
			index = end + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(value[index:])
		for width := shell.StringWidth(value[index : index+size]); width > 0; width-- {
			cells = append(cells, bold)
		}
		index += size
	}
	return cells
}

func selectedLineContaining(t *testing.T, model Model, text string) string {
	t.Helper()
	for _, line := range strings.Split(model.View(), "\n") {
		if strings.HasPrefix(line, selectedRowPrefix()) && strings.Contains(shell.Plain(line), text) {
			return line
		}
	}
	t.Fatalf("no selected line contains %q: %q", text, model.View())
	return ""
}

func assertTextBold(t *testing.T, line, text string) {
	t.Helper()
	runes := []rune(shell.Plain(line))
	cells := boldCells(line)
	target := []rune(text)
	for index := 0; index+len(target) <= len(runes); index++ {
		if string(runes[index:index+len(target)]) != text {
			continue
		}
		for _, bold := range cells[index : index+len(target)] {
			if !bold {
				t.Fatalf("text %q is not bold on the selected row: %q", text, line)
			}
		}
		return
	}
	t.Fatalf("text %q not found in the selected row: %q", text, line)
}

// TestSelectedFolderRowKeepsBold pins that a selected folder header keeps the
// bold weight it has when unselected instead of falling back to normal text.
func TestSelectedFolderRowKeepsBold(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	model := NewModel(context.Background(), Deps{Projects: []picker.Place{
		{Kind: picker.ProjectKind, Path: "/work/nexo", Group: "/work", DisplayRemainder: "nexo"},
	}}, ProjectsTab)
	model.SetSize(80, 10)
	if model.Cursor() != "group:/work" {
		t.Fatalf("cursor = %q, want the folder header", model.Cursor())
	}
	assertTextBold(t, selectedLineContaining(t, model, "/work"), "/work")
}

// TestSelectedAgentRowKeepsBold covers the other row kind that is bold while
// unselected: the harness name on an agent row.
func TestSelectedAgentRowKeepsBold(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{
		{PaneID: "a", Container: "one", ContainerID: "one", Harness: "pi", Target: "one:0.1", State: "working"},
	}}, AgentsTab)
	model.SetSize(80, 10)
	if !strings.HasPrefix(model.Cursor(), "agent:") {
		t.Fatalf("cursor = %q, want an agent", model.Cursor())
	}
	assertTextBold(t, selectedLineContaining(t, model, "pi"), "pi")
}

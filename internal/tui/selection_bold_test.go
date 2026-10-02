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
	assertTextWeight(t, line, text, true)
}

func assertTextNotBold(t *testing.T, line, text string) {
	t.Helper()
	assertTextWeight(t, line, text, false)
}

func assertTextWeight(t *testing.T, line, text string, want bool) {
	t.Helper()
	runes := []rune(shell.Plain(line))
	cells := boldCells(line)
	target := []rune(text)
	for index := 0; index+len(target) <= len(runes); index++ {
		if string(runes[index:index+len(target)]) != text {
			continue
		}
		for _, bold := range cells[index : index+len(target)] {
			if bold != want {
				t.Fatalf("bold(%q) = %v, want %v: %q", text, bold, want, line)
			}
		}
		return
	}
	t.Fatalf("text %q not found: %q", text, line)
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
// unselected: the container name on an agent row.
func TestSelectedAgentRowKeepsBold(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{
		{PaneID: "a", Container: "one", ContainerID: "one", Harness: "pi", Target: "one:0.1", State: "working"},
	}}, AgentsTab)
	model.SetSize(80, 10)
	if !strings.HasPrefix(model.Cursor(), "agent:") {
		t.Fatalf("cursor = %q, want an agent", model.Cursor())
	}
	assertTextBold(t, selectedLineContaining(t, model, "one"), "one")
}

// TestAgentRowLeadsWithTheContainerName pins the agent row order and style: the
// bold container name comes first, the muted harness name second, separated by
// the same two-space gap.
func TestAgentRowLeadsWithTheContainerName(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{
		{PaneID: "a", Container: "personal_nexo", ContainerID: "personal_nexo", Harness: "claude", Target: "personal_nexo:0.1", State: "working"},
	}}, AgentsTab)
	model.SetSize(100, 10)
	agent, ok := model.frame().currentItem()
	if !ok || agent.itemKind != "agent" {
		t.Fatalf("cursor = %q, want an agent row", model.Cursor())
	}
	primary, _ := model.frame().rowText(agent)
	plain := shell.Plain(primary)
	if !strings.Contains(plain, "personal_nexo  claude") {
		t.Fatalf("agent row = %q, want the container before the harness", plain)
	}
	if strings.Contains(plain, "claude  personal_nexo") {
		t.Fatalf("agent row = %q, want the harness after the container", plain)
	}
	assertTextBold(t, primary, "personal_nexo")
	assertTextNotBold(t, primary, "claude")
	if !strings.Contains(primary, muted().Render("  claude")) {
		t.Fatalf("harness is not muted: %q", primary)
	}
}

// TestAgentRowKeepsQueryHighlightingOnBothNames pins that a query matching
// either the container or the harness still highlights that field after the
// swap.
func TestAgentRowKeepsQueryHighlightingOnBothNames(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{
		{PaneID: "a", Container: "personal_nexo", ContainerID: "personal_nexo", Harness: "claude", Target: "personal_nexo:0.1", State: "working"},
	}}, AgentsTab)
	model.SetSize(100, 10)
	agent, ok := model.frame().currentItem()
	if !ok || agent.itemKind != "agent" {
		t.Fatalf("cursor = %q, want an agent row", model.Cursor())
	}
	for _, query := range []string{"personal_nexo", "claude"} {
		frame := model.frame()
		frame.query = []rune(query)
		primary, _ := frame.agentText(agent, "  ", false)
		matched := accent().Bold(true).Render(query[:1])
		if !strings.Contains(primary, matched) {
			t.Fatalf("query %q did not highlight the matched field: %q", query, primary)
		}
	}
}

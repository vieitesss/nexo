package navigator

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

func TestFullScreenViewHonorsWindowSize(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents([]capabilities.Agent{
		{PaneID: "one", Target: "workspace:window.1", Harness: "pi", Container: "workspace", Window: "window", State: "working"},
		{PaneID: "two", Target: "workspace:window.2", Harness: "claude", Container: "workspace", Window: "window", State: "stopped"},
	})
	selected := model.Rows()[model.Cursor()].Agent.PaneID
	for _, size := range [][2]int{{200, 50}, {100, 30}, {80, 24}, {40, 6}, {40, 4}} {
		updated, _ := model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model = updated.(Model)
		assertNavigatorSurfaceSize(t, model.View(), size[0], size[1])
		if got := model.Rows()[model.Cursor()].Agent.PaneID; got != selected {
			t.Fatalf("selected pane = %q after resize, want %q", got, selected)
		}
	}
}

func TestRefreshKeepsNavigatorSelectionByPaneIdentity(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents([]capabilities.Agent{
		{PaneID: "one", Target: "one", Harness: "pi", State: "working"},
		{PaneID: "two", Target: "two", Harness: "pi", State: "working"},
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	selected := model.Rows()[model.Cursor()].Agent.PaneID
	updated, _ = model.Update(RefreshMsg{Agents: []capabilities.Agent{
		{PaneID: "two", Target: "two", Harness: "pi", State: "working"},
		{PaneID: "one", Target: "one", Harness: "pi", State: "working"},
	}})
	model = updated.(Model)
	if got := model.Rows()[model.Cursor()].Agent.PaneID; got != selected {
		t.Fatalf("selected pane = %q, want %q after refresh", got, selected)
	}
}

func TestNavigatorLongQueryKeepsPromptAndCursor(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents([]capabilities.Agent{{PaneID: "one", Target: "workspace", Harness: "pi", State: "working"}})
	model.SetQuery("a very long query that exceeds the frame")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	model = updated.(Model)
	first := strings.Split(model.View(), "\n")[0]
	first = strings.TrimRight(first, " ")
	if !strings.HasPrefix(first, "Search: ") || !strings.HasSuffix(first, "▌") {
		t.Fatalf("long query line = %q, want prompt and cursor", first)
	}
}

func TestNavigatorDegenerateResizeDoesNotRestoreLegacyFrame(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	model = updated.(Model)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	model = updated.(Model)
	if got := model.View(); got != "" {
		t.Fatalf("degenerate full-screen view = %q, want empty frame", got)
	}
}

func assertNavigatorSurfaceSize(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Fatalf("surface has %d rows, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("surface row %d has width %d, want %d: %q", index, got, width, line)
		}
	}
}

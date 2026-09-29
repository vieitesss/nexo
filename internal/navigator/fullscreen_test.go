package navigator

import (
	"fmt"
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

func TestNavigatorManyRowsScrollsAndRefreshKeepsSelection(t *testing.T) {
	agents := make([]capabilities.Agent, 12)
	for index := range agents {
		agents[index] = capabilities.Agent{PaneID: fmt.Sprintf("pane-%02d", index), Target: fmt.Sprintf("api:%02d.0", index), Harness: "pi", State: "working"}
	}
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents(agents)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	model = updated.(Model)
	for range 9 {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	selected := model.Rows()[model.Cursor()].Agent.PaneID
	selectedTarget := model.Rows()[model.Cursor()].Agent.Target
	view := model.View()
	if !strings.Contains(view, selectedTarget) {
		t.Fatalf("scrolled view hides selected agent %q: %q", selectedTarget, view)
	}
	if strings.Contains(view, "api:00.0") {
		t.Fatalf("scrolled view still starts at the first agent: %q", view)
	}
	reversed := append([]capabilities.Agent(nil), agents...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	updated, _ = model.Update(RefreshMsg{Agents: reversed})
	model = updated.(Model)
	if got := model.Rows()[model.Cursor()].Agent.PaneID; got != selected {
		t.Fatalf("selected pane = %q after refresh, want %q", got, selected)
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

func TestNavigatorMetadataIsIndentedAndFieldBudgeted(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 3, 0, 0, time.UTC)
	stopped := now.Add(-3 * time.Minute)
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetClock(func() time.Time { return now })
	model.SetAgents([]capabilities.Agent{
		{PaneID: "one", Target: "api:0.0", Harness: "pi", Container: "api", Window: "window", State: "working", Branch: "main"},
		{PaneID: "two", Target: "api:1.0", Harness: "claude", Container: "api", Window: "window", State: "stopped", StoppedSince: &stopped, Branch: "main"},
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	model = updated.(Model)
	lines := strings.Split(model.View(), "\n")
	if !strings.HasPrefix(lines[2], "  │ ") {
		t.Fatalf("continuing metadata lacks muted indentation prefix: %q", lines[2])
	}
	if !strings.HasPrefix(lines[5], "    ") {
		t.Fatalf("last metadata is not aligned under the primary item: %q", lines[5])
	}

	long := NewModel(nil, picker.Theme{}, time.Second)
	long.SetClock(func() time.Time { return now })
	long.SetAgents([]capabilities.Agent{{
		PaneID: "long", Target: "api-target-api-target-api-target", Harness: "pi",
		Window: "long-window-long-window-long-window-long-window", State: "stopped",
		StoppedSince: &stopped, Branch: "main",
	}})
	updated, _ = long.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	long = updated.(Model)
	secondary := strings.Split(long.View(), "\n")[2]
	if !strings.HasPrefix(secondary, "    ") {
		t.Fatalf("single-row metadata lacks alignment prefix: %q", secondary)
	}
	if !strings.Contains(secondary, "l") || !strings.Contains(secondary, "a") || !strings.Contains(secondary, "⎇ main") || !strings.Contains(secondary, "3m") {
		t.Fatalf("metadata fields were not independently budgeted: %q", secondary)
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

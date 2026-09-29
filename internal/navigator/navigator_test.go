package navigator

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

func testAgents() []capabilities.Agent {
	stopped := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	seenStopped := stopped.Add(-time.Minute)
	return []capabilities.Agent{
		{PaneID: "%3", Target: "zeta:2.1", Harness: "pi", State: "working", Branch: "main"},
		{PaneID: "%1", Target: "beta:1.2", Harness: "claude", State: "stopped", Seen: false, StoppedSince: &stopped, Branch: "feature/navigator"},
		{PaneID: "%2", Target: "alpha:3.4", Harness: "codex", State: "stopped", Seen: true, StoppedSince: &seenStopped},
	}
}

func TestModelSortsByAttentionThenTarget(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC)
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetClock(func() time.Time { return now })
	model.SetAgents(testAgents())
	rows := model.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows = %#v, want three agents", rows)
	}
	want := []string{"beta:1.2", "zeta:2.1", "alpha:3.4"}
	for i, target := range want {
		if rows[i].Agent.Target != target {
			t.Fatalf("row %d target = %q, want %q", i, rows[i].Agent.Target, target)
		}
	}
}

func TestModelFiltersWithoutBlockingRefresh(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents(testAgents())
	model.SetQuery("feature")
	rows := model.Rows()
	if len(rows) != 1 || rows[0].Agent.PaneID != "%1" {
		t.Fatalf("rows = %#v, want feature branch match", rows)
	}
}

func TestGoldenView(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 30, 0, time.UTC)
	stopped := now.Add(-30 * time.Second)
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetClock(func() time.Time { return now })
	model.SetAgents([]capabilities.Agent{
		{PaneID: "%1", Target: "work:1.2", Container: "work", Window: "1", Harness: "pi", State: "stopped", StoppedSince: &stopped, Branch: "feature/demo"},
		{PaneID: "%2", Target: "work:1.3", Container: "work", Window: "1", Harness: "claude", State: "working"},
	})
	want := "> \n● pi · work · 1 · work:1.2 · ⎇ feature/demo  30s\n● claude · work · 1 · work:1.3"
	if got := model.View(); got != want {
		t.Fatalf("View() = %q, want %q", got, want)
	}
}

func TestInitMarksRefreshInFlightBeforeFirstCommand(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	if *model.refreshInFlight {
		t.Fatal("new model starts with refresh in flight")
	}
	model.Init()
	if !*model.refreshInFlight {
		t.Fatal("Init did not mark refresh in flight")
	}
	updated, cmd := model.Update(refreshTickMsg{})
	if cmd == nil || !*updated.(Model).refreshInFlight {
		t.Fatal("tick started a concurrent initial refresh")
	}
}

func TestRefreshResultReplacesAgentsAndSchedulesNextTick(t *testing.T) {
	source := fakeSource{agents: []capabilities.Agent{{PaneID: "%1", Target: "work:1.1", Harness: "pi", State: "working"}}}
	model := NewModel(source, picker.Theme{}, time.Millisecond)
	updated, cmd := model.Update(RefreshMsg{Agents: source.agents})
	model = updated.(Model)
	if len(model.Rows()) != 1 || cmd != nil {
		t.Fatalf("rows=%#v cmd=%v, want refreshed row without blocking", model.Rows(), cmd)
	}
}

func TestCancelKeysQuitWithoutSelection(t *testing.T) {
	for _, keyType := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEsc} {
		model := NewModel(nil, picker.Theme{}, time.Second)
		updated, cmd := model.Update(tea.KeyMsg{Type: keyType})
		model = updated.(Model)
		if cmd == nil || !model.Cancelled() {
			t.Fatalf("key %v: cancelled=%v cmd=%v", keyType, model.Cancelled(), cmd)
		}
	}
}

func TestEnterSelectsExactPane(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents([]capabilities.Agent{{PaneID: "%7", Target: "work:1.2", Harness: "pi", State: "working"}})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter did not quit")
	}
	agent, ok := model.Selected()
	if !ok || agent.PaneID != "%7" {
		t.Fatalf("selected = %#v, ok=%v", agent, ok)
	}
}

type fakeSource struct{ agents []capabilities.Agent }

func (f fakeSource) ListAgents(context.Context) ([]capabilities.Agent, error) { return f.agents, nil }

func TestRowFormattingUsesTargetContainerAndWindow(t *testing.T) {
	model := NewModel(nil, picker.Theme{}, time.Second)
	model.SetAgents([]capabilities.Agent{{PaneID: "pane-1", Target: "workspace:tab.1", Harness: "pi", State: "working"}})
	if got, want := strings.TrimSpace(model.View()), "> \n● pi · workspace:tab.1"; got != want {
		t.Fatalf("View() = %q, want %q", got, want)
	}
}

package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

func TestSourceAcceptOwnsStaleResultRejection(t *testing.T) {
	model := NewModel(context.Background(), Deps{Agents: []capabilities.Agent{{PaneID: "old", Target: "old"}}}, AgentsTab)
	source := model.source
	source.generations.agents = 2
	if _, ok := source.Accept(agentsMsg{generation: 1, agents: []capabilities.Agent{{PaneID: "stale"}}}); ok {
		t.Fatal("stale source result was accepted")
	}
	if got := source.agents[0].agent.PaneID; got != "old" {
		t.Fatalf("stale result replaced source state: %q", got)
	}
	update, ok := source.Accept(agentsMsg{generation: 2, agents: []capabilities.Agent{{PaneID: "new", Target: "new"}}})
	if !ok || !update.changed {
		t.Fatalf("fresh source result = (%#v, %t), want changed acceptance", update, ok)
	}
	if got := source.agents[0].agent.PaneID; got != "new" {
		t.Fatalf("fresh result did not replace source state: %q", got)
	}
}

func TestInputTransitionOwnsQueryAndFoldingState(t *testing.T) {
	input := newInputState(ProjectsTab, Deps{}, context.Background())
	frame := inputFrame{height: 10}
	effect := input.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, frame)
	if !effect.rebuild || string(input.query) != "n" {
		t.Fatalf("query transition = %#v, query %q", effect, input.query)
	}
	rows := []row{{id: "group:/work", kind: groupRow, cursorable: true, foldable: true}}
	input.cursor = rows[0].id
	input.query = nil
	effect = input.key(tea.KeyMsg{Type: tea.KeyRight}, inputFrame{rows: rows})
	if !effect.rebuild || !input.expanded[rows[0].id] {
		t.Fatalf("fold transition = %#v, expanded=%v", effect, input.expanded)
	}
}

// TestInputDefersPresentationUntilNeeded pins the lazy frame contract: blocked
// and motion events are rejected before any presentation input is built,
// ordinary keyboard transitions build nothing, and a real mouse hit test
// builds exactly one layout frame.
func TestInputDefersPresentationUntilNeeded(t *testing.T) {
	rows := []row{{
		id: "container:local:one", kind: itemRow, itemKind: "container", cursorable: true,
		place: picker.Place{Kind: picker.ContainerKind, Machine: picker.LocalMachine(), ContainerID: "one"},
	}}
	builds := 0
	inputFrameFor := func() inputFrame {
		builds = 0
		return inputFrame{
			rows:   rows,
			height: 10,
			marked: func() []picker.ContainerRef { builds++; return nil },
			render: func() renderFrame {
				builds++
				return renderFrame{width: 80, height: 10, sized: true, tab: ContainersTab, rows: rows}
			},
		}
	}
	assertNoBuild := func(t *testing.T) {
		t.Helper()
		if builds != 0 {
			t.Fatalf("event built %d presentation inputs, want none", builds)
		}
	}

	t.Run("blocked key", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.actionInFlight = true
		effect := input.key(tea.KeyMsg{Type: tea.KeyDown}, inputFrameFor())
		if effect.rebuild || effect.quit || effect.cmd != nil {
			t.Fatalf("blocked key effect = %#v, want empty", effect)
		}
		assertNoBuild(t)
	})

	t.Run("blocked ctrl-c still quits", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.actionInFlight = true
		if effect := input.key(tea.KeyMsg{Type: tea.KeyCtrlC}, inputFrameFor()); !effect.quit {
			t.Fatalf("ctrl-c effect = %#v, want quit", effect)
		}
		assertNoBuild(t)
	})

	t.Run("blocked mouse", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.renameMode = true
		input.mouse(tea.MouseMsg{X: 4, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, inputFrameFor())
		assertNoBuild(t)
	})

	t.Run("mouse motion", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.mouse(tea.MouseMsg{X: 4, Y: 2, Action: tea.MouseActionMotion}, inputFrameFor())
		assertNoBuild(t)
	})

	t.Run("wheel navigation", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.mouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown}, inputFrameFor())
		assertNoBuild(t)
	})

	t.Run("navigation key", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.key(tea.KeyMsg{Type: tea.KeyDown}, inputFrameFor())
		assertNoBuild(t)
	})

	t.Run("ctrl-x marked lookup", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.key(tea.KeyMsg{Type: tea.KeyCtrlX}, inputFrameFor())
		if builds != 1 {
			t.Fatalf("ctrl-x built %d presentation inputs, want one marked lookup", builds)
		}
	})

	t.Run("left press hit test", func(t *testing.T) {
		input := newInputState(ContainersTab, Deps{}, context.Background())
		input.mouse(tea.MouseMsg{X: 4, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}, inputFrameFor())
		if builds != 1 {
			t.Fatalf("left press built %d presentation inputs, want one layout", builds)
		}
	})
}

func TestRenderFrameHitTestUsesItsProjectionAndLayout(t *testing.T) {
	projection := rowProjection{}
	frame := renderFrame{
		width: 80, height: 6, sized: true, tab: ContainersTab,
		rows:       []row{{id: "group", kind: groupRow, cursorable: true}},
		projection: projection,
	}
	if hit := frame.hitTest(0, 0); hit.tab != AgentsTab {
		t.Fatalf("tab hit = %#v, want agents", hit)
	}
	if hit := frame.hitTest(0, 2); hit.rowID != "group" {
		t.Fatalf("row hit = %#v, want group", hit)
	}
}

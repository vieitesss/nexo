package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
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
	frame := inputFrame{render: renderFrame{width: 80, height: 10}}
	effect := input.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}, frame)
	if !effect.rebuild || string(input.query) != "n" {
		t.Fatalf("query transition = %#v, query %q", effect, input.query)
	}
	rows := []row{{id: "group:/work", kind: groupRow, cursorable: true, foldable: true}}
	input.cursor = rows[0].id
	input.query = nil
	effect = input.key(tea.KeyMsg{Type: tea.KeyRight}, inputFrame{render: frame.render, rows: rows})
	if !effect.rebuild || !input.expanded[rows[0].id] {
		t.Fatalf("fold transition = %#v, expanded=%v", effect, input.expanded)
	}
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

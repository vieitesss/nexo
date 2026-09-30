package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestKillConfirmationUsesMarkedContainers(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlT})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlX})
	if _, ok := model.KillTargetsRequest(); ok {
		t.Fatal("kill became ready before confirmation")
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	refs, ok := model.KillTargetsRequest()
	if !ok || len(refs) != 1 || refs[0].ID != "one" {
		t.Fatalf("kill targets = %v, %v", refs, ok)
	}
}

func TestAgentIdentityStaysSelectedAcrossRefresh(t *testing.T) {
	agents := []capabilities.Agent{
		{PaneID: "two", Target: "two:0.1", Harness: "pi", State: "working"},
		{PaneID: "one", Target: "one:0.1", Harness: "pi", State: "working"},
	}
	model := NewModel(context.Background(), Deps{Agents: agents}, AgentsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.Cursor() != "agent:two" {
		t.Fatalf("cursor = %q, want the second agent identity", model.Cursor())
	}
	selected := model.Cursor()
	model.SetAgents([]capabilities.Agent{agents[1], agents[0]})
	if model.Cursor() != selected {
		t.Fatalf("cursor changed identity from %q to %q", selected, model.Cursor())
	}
}

func TestRenamePromptRequiresContainer(t *testing.T) {
	model := NewModel(context.Background(), testContainerDeps(), ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n', 'e', 'w'}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	ref, name, ok := model.RenameTargetRequest()
	if !ok || ref.ID != "one" || name != "new" {
		t.Fatalf("rename = %v, %q, %v", ref, name, ok)
	}
}

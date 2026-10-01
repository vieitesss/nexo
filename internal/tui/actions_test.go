package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
)

func TestKillConfirmationRunsInTUI(t *testing.T) {
	deps := testContainerDeps()
	var got []picker.ContainerRef
	deps.KillContainers = func(_ context.Context, refs []picker.ContainerRef) error {
		got = append([]picker.ContainerRef(nil), refs...)
		return nil
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlT})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlX})
	if !model.input.confirmKill {
		t.Fatal("kill did not enter confirmation")
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil || !model.input.actionInFlight {
		t.Fatalf("kill did not start in TUI: cmd=%v model=%+v", cmd != nil, model)
	}
	model = updateModel(t, model, cmd())
	if len(got) != 1 || got[0].ID != "one" || len(model.Marked()) != 0 || model.toast != "containers killed" {
		t.Fatalf("kill result = refs %v, marks %v, toast %q", got, model.Marked(), model.toast)
	}
}

func TestAgentIdentityStaysSelectedAcrossRefresh(t *testing.T) {
	agents := []capabilities.Agent{
		{PaneID: "two", Target: "two:0.1", Harness: "pi", State: "working"},
		{PaneID: "one", Target: "one:0.1", Harness: "pi", State: "working"},
	}
	model := NewModel(context.Background(), Deps{Agents: agents}, AgentsTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.HasSuffix(model.Cursor(), ":two") {
		t.Fatalf("cursor = %q, want the second agent identity", model.Cursor())
	}
	selected := model.Cursor()
	model.SetAgents([]capabilities.Agent{agents[1], agents[0]})
	if model.Cursor() != selected {
		t.Fatalf("cursor changed identity from %q to %q", selected, model.Cursor())
	}
}

func TestRenamePromptRunsInTUI(t *testing.T) {
	deps := testContainerDeps()
	var gotRef picker.ContainerRef
	var gotName string
	deps.RenameContainer = func(_ context.Context, ref picker.ContainerRef, name string) error {
		gotRef, gotName = ref, name
		return nil
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n', 'e', 'w'}})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil || !model.input.actionInFlight {
		t.Fatal("rename did not start in TUI")
	}
	model = updateModel(t, model, cmd())
	if gotRef.ID != "one" || gotName != "new" || model.toast != "container renamed" {
		t.Fatalf("rename = %v, %q, toast %q", gotRef, gotName, model.toast)
	}
}

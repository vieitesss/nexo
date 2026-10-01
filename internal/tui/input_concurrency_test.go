package tui

// Command closures cross Bubble Tea's command goroutine while Update keeps
// handling input on the program goroutine. These tests pin the contract that a
// command captures its callback when it is built instead of reading the
// transition state later. ThreadSanitizer cannot run on every host, so the
// defect is made deterministic by rewriting the shared dependencies after
// construction: a command that still reads the live state would run the
// replacement.

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/picker"
)

func TestKillCommandCapturesCallbackAtConstruction(t *testing.T) {
	deps := testContainerDeps()
	var killed []picker.ContainerRef
	deps.KillContainers = func(_ context.Context, refs []picker.ContainerRef) error {
		killed = append([]picker.ContainerRef(nil), refs...)
		return nil
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyCtrlX))
	updated, cmd := model.Update(keyMsg(tea.KeyRunes, 'y'))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("kill command missing")
	}

	// Every later input event rewrites this shared struct. The closure must
	// keep the callback captured while its command was built.
	model.input.deps.KillContainers = func(context.Context, []picker.ContainerRef) error {
		t.Error("kill command read transition-state dependencies built after it")
		return nil
	}
	action, ok := cmd().(actionMsg)
	if !ok || action.err != nil {
		t.Fatalf("kill command result = %#v, want successful action", action)
	}
	if len(killed) != 1 || killed[0].ID != "one" {
		t.Fatalf("killed = %v, want the container captured at construction", killed)
	}
}

func TestRenameCommandCapturesCallbackAtConstruction(t *testing.T) {
	deps := testContainerDeps()
	var renamed picker.ContainerRef
	deps.RenameContainer = func(_ context.Context, ref picker.ContainerRef, _ string) error {
		renamed = ref
		return nil
	}
	model := NewModel(context.Background(), deps, ContainersTab)
	model = updateModel(t, model, keyMsg(tea.KeyCtrlR))
	for _, value := range []rune{'n', 'e', 'w'} {
		model = updateModel(t, model, keyMsg(tea.KeyRunes, value))
	}
	updated, cmd := model.Update(keyMsg(tea.KeyEnter))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("rename command missing")
	}

	model.input.deps.RenameContainer = func(context.Context, picker.ContainerRef, string) error {
		t.Error("rename command read transition-state dependencies built after it")
		return nil
	}
	action, ok := cmd().(actionMsg)
	if !ok || action.err != nil {
		t.Fatalf("rename command result = %#v, want successful action", action)
	}
	if renamed.ID != "one" {
		t.Fatalf("renamed = %v, want the container captured at construction", renamed)
	}
}

package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

// machineHeaderScreenY locates the first non-hit-testable body line that starts
// with the machine label, so a test can click the header exactly where it is
// painted.
func machineHeaderScreenY(t *testing.T, model Model, machine string) int {
	t.Helper()
	label := shortenMachine(machine)
	lines, start := model.frame().bodyLayout(model.height - 3)
	for index := start; index < len(lines); index++ {
		if lines[index].rowID != "" {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(shell.Plain(lines[index].text), " "), label) {
			return 2 + index - start
		}
	}
	t.Fatalf("machine header %q not found in body; rows=%v", machine, model.Rows())
	return -1
}

// TestMachineHeadersAreSkippedByKeyboardAndMouse pins the rule that a machine
// section header is not a selectable row: no keyboard step and no click may
// land the cursor on it.
func TestMachineHeadersAreSkippedByKeyboardAndMouse(t *testing.T) {
	local := picker.LocalMachine()
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{
		{ID: "one", Name: "one", Machine: local},
		{ID: "two", Name: "two", Machine: local},
		{ID: "dev-one", Name: "dev-one", Machine: "dev"},
	}}, ContainersTab)
	model.SetSize(80, 24)
	if !strings.HasPrefix(model.Cursor(), "container:") {
		t.Fatalf("initial cursor = %q, want a container", model.Cursor())
	}
	for range len(model.Rows()) + 2 {
		model = updateModel(t, model, keyMsg(tea.KeyUp))
		if strings.HasPrefix(model.Cursor(), "machine:") {
			t.Fatalf("Up selected a machine header: %q", model.Cursor())
		}
	}
	for range len(model.Rows()) + 2 {
		model = updateModel(t, model, keyMsg(tea.KeyDown))
		if strings.HasPrefix(model.Cursor(), "machine:") {
			t.Fatalf("Down selected a machine header: %q", model.Cursor())
		}
	}

	cursor, rows := model.Cursor(), append([]string(nil), model.Rows()...)
	y := machineHeaderScreenY(t, model, local)
	model = updateModel(t, model, tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Cursor() != cursor || strings.Join(model.Rows(), "\x00") != strings.Join(rows, "\x00") {
		t.Fatalf("click on machine header changed state: cursor %q -> %q, rows %v -> %v", cursor, model.Cursor(), rows, model.Rows())
	}
}

// TestAgentMachineHeadersAreSkippedByKeyboardAndMouse covers the Agents tab,
// where a machine header is followed by two-line agent rows.
func TestAgentMachineHeadersAreSkippedByKeyboardAndMouse(t *testing.T) {
	model := NewModel(context.Background(), richDeps(), AgentsTab)
	model.SetSize(100, 24)
	if !strings.HasPrefix(model.Cursor(), "agent:") {
		t.Fatalf("initial cursor = %q, want an agent", model.Cursor())
	}
	for range len(model.Rows()) + 2 {
		model = updateModel(t, model, keyMsg(tea.KeyUp))
		if strings.HasPrefix(model.Cursor(), "machine:") {
			t.Fatalf("Up selected a machine header: %q", model.Cursor())
		}
	}

	cursor := model.Cursor()
	y := machineHeaderScreenY(t, model, picker.LocalMachine())
	model = updateModel(t, model, tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if model.Cursor() != cursor {
		t.Fatalf("click on agent machine header moved cursor: %q -> %q", cursor, model.Cursor())
	}
}

// TestErroredMachineSectionLeavesNoCursor is the failure half of the empty-
// section rule: a machine whose listing failed shows its error and still has no
// selectable row to park the cursor on.
func TestErroredMachineSectionLeavesNoCursor(t *testing.T) {
	model := NewModel(context.Background(), Deps{RemoteMachines: []string{"dev"}}, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Error: "offline"})
	model.SetSize(80, 12)
	if model.Cursor() != "" {
		t.Fatalf("cursor = %q, want none when the only section failed", model.Cursor())
	}
	if !strings.Contains(shell.Plain(model.View()), "offline") {
		t.Fatalf("failed section did not render its error: %q", model.View())
	}
}

// TestEmptyMachineSectionsLeaveNoCursor keeps startup selection honest when a
// section has no selectable row yet: nothing is selected, and navigation and
// Enter do nothing instead of parking the cursor on a header.
func TestEmptyMachineSectionsLeaveNoCursor(t *testing.T) {
	model := NewModel(context.Background(), Deps{RemoteMachines: []string{"dev"}}, ContainersTab)
	model.SetSize(80, 12)
	if model.Cursor() != "" {
		t.Fatalf("cursor = %q, want none when every section is empty", model.Cursor())
	}
	if !strings.Contains(shell.Plain(model.View()), "loading…") {
		t.Fatalf("loading remote section not rendered: %q", model.View())
	}
	for _, key := range []tea.KeyType{tea.KeyUp, tea.KeyDown, tea.KeyEnter} {
		model = updateModel(t, model, keyMsg(key))
		if model.Cursor() != "" {
			t.Fatalf("%v moved the cursor to %q in an empty body", key, model.Cursor())
		}
	}
	if _, ok := model.SelectedPlace(); ok {
		t.Fatal("Enter selected a place with no selectable row")
	}
}

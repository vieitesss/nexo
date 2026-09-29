package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestRemoteBatchAddsContainersAndAgentDots(t *testing.T) {
	model := NewModel([]Place{MachinePlace("local")}, DefaultTheme)
	updated, _ := model.Update(RemoteBatch{
		Machine: "devbox",
		Places:  []Place{{Kind: ContainerKind, Machine: "devbox", Group: "devbox", ContainerID: "$1", ContainerName: "api", DisplayRemainder: "api", SearchText: "api"}},
		Agents:  []capabilities.Agent{{Container: "api", State: "stopped", Seen: false}},
	})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if len(model.Rows()) != 4 || len(model.Rows()[2].Place.Decorations) != 1 || model.Rows()[2].Place.Decorations[0].Style != DecorationRed {
		t.Fatalf("rows = %#v, want remote container with urgent dot", model.Rows())
	}
}

func TestRemoteBatchAddsContainersAndUnreachableHeader(t *testing.T) {
	model := NewModel([]Place{MachinePlace("local")}, DefaultTheme)
	updated, _ := model.Update(RemoteBatch{
		Machine: "devbox",
		Places:  []Place{{Kind: ContainerKind, Machine: "devbox", Group: "devbox", ContainerID: "$1", ContainerName: "api", DisplayRemainder: "api", SearchText: "api"}},
	})
	model = updated.(Model)
	updated, _ = model.Update(RemoteBatch{Machine: "gpu1", Error: "connection refused"})
	model = updated.(Model)
	rows := model.Rows()
	if len(rows) != 3 || rows[1].Group != "devbox" || rows[2].Group != "gpu1" || rows[2].Error != "connection refused" {
		t.Fatalf("rows = %#v, want local, devbox, and failed gpu1 headers", rows)
	}
	if !strings.Contains(model.View(), "gpu1 — connection refused") {
		t.Fatalf("view = %q, want unreachable reason", model.View())
	}
}

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

func TestRemoteAgentDotsUseStableContainerIdentityAndAllowDuplicateLabels(t *testing.T) {
	model := NewModel([]Place{MachinePlace(LocalMachine())}, DefaultTheme)
	updated, _ := model.Update(RemoteBatch{
		Machine: "devbox",
		Places: []Place{
			{Kind: ContainerKind, Machine: "devbox", Group: "devbox", ContainerID: "w1", ContainerName: "api", SearchText: "api", DisplayRemainder: "api"},
			{Kind: ContainerKind, Machine: "devbox", Group: "devbox", ContainerID: "w2", ContainerName: "api", SearchText: "api", DisplayRemainder: "api"},
		},
		Agents: []capabilities.Agent{{ContainerID: "w1", State: "stopped", Seen: false}},
	})
	model = updated.(Model)
	places := model.Places()
	if len(places) != 3 {
		t.Fatalf("places = %#v, want local plus two remote containers", places)
	}
	if len(places[1].Decorations) != 1 || places[1].Decorations[0].Style != DecorationRed {
		t.Fatalf("first duplicate-label place = %#v, want urgent dot", places[1])
	}
	if len(places[2].Decorations) != 0 {
		t.Fatalf("second duplicate-label place = %#v, want no dot", places[2])
	}
}

func TestAgentBatchesStayScopedToTheirSourceMachineRegardlessOfArrivalOrder(t *testing.T) {
	local := LocalMachine()
	localPlace := Place{Kind: ContainerKind, Machine: local, Group: local, ContainerID: "same", ContainerName: "api", SearchText: "api", DisplayRemainder: "api"}
	model := NewModel([]Place{localPlace}, DefaultTheme)
	remoteBatch := RemoteBatch{
		Machine: "devbox",
		Places:  []Place{{Kind: ContainerKind, Machine: "devbox", Group: "devbox", ContainerID: "same", ContainerName: "api", SearchText: "api", DisplayRemainder: "api"}},
		Agents:  []capabilities.Agent{{ContainerID: "same", State: "stopped", Seen: false}},
	}
	updated, _ := model.Update(remoteBatch)
	model = updated.(Model)
	updated, _ = model.Update(AgentBatch{})
	model = updated.(Model)
	places := model.Places()
	if len(places) != 2 || len(places[1].Decorations) != 1 || places[1].Decorations[0].Style != DecorationRed {
		t.Fatalf("remote-first places = %#v, want remote dot after empty local batch", places)
	}

	model = NewModel([]Place{localPlace}, DefaultTheme)
	updated, _ = model.Update(AgentBatch{Agents: []capabilities.Agent{{ContainerID: "same", State: "working"}}})
	model = updated.(Model)
	updated, _ = model.Update(remoteBatch)
	model = updated.(Model)
	places = model.Places()
	if len(places) != 2 {
		t.Fatalf("local-first places = %#v, want two containers", places)
	}
	if len(places[0].Decorations) != 1 || places[0].Decorations[0].Style != DecorationYellow {
		t.Fatalf("local place = %#v, want working dot", places[0])
	}
	if len(places[1].Decorations) != 1 || places[1].Decorations[0].Style != DecorationRed {
		t.Fatalf("remote place = %#v, want urgent dot", places[1])
	}
}

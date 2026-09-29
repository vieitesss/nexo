package picker

import (
	"strings"
	"testing"
)

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

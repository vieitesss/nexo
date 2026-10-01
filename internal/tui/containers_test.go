package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func TestContainersAreOneLineAndNeverRenderPreview(t *testing.T) {
	since := time.Now().Add(-3 * time.Minute)
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "one", Name: "one", Machine: picker.LocalMachine(), Preview: "garbage pane line\nsecond line"}}, Agents: []capabilities.Agent{{PaneID: "p", ContainerID: "one", Container: "one", Harness: "pi", State: "stopped", StoppedSince: &since}}}, ContainersTab)
	model.SetSize(120, 12)
	plain := shell.Plain(model.View())
	if strings.Contains(plain, "garbage pane") || strings.Contains(plain, "no agents") || strings.Contains(plain, "└") {
		t.Fatalf("container frame retained old secondary/tree text: %q", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "one") && strings.Contains(line, "●") {
			if strings.Contains(line, "stopped") || strings.Contains(line, "3m") {
				t.Fatalf("container row rendered agent details: %q", line)
			}
			return
		}
	}
	t.Fatal("container row not found")
}

func TestContainerRowsRenderOnlyStateDots(t *testing.T) {
	old := time.Now().Add(-3 * time.Minute)
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "long", Name: "a-very-long-container-name-that-needs-truncation", Machine: picker.LocalMachine(), Activity: &old}}, Agents: []capabilities.Agent{{PaneID: "a", ContainerID: "long", Container: "long", Harness: "pi", State: "working"}, {PaneID: "b", ContainerID: "long", Container: "long", Harness: "claude", State: "stopped", Seen: true, StoppedSince: &old}}}, ContainersTab)
	model.SetSize(120, 8)
	full := shell.Plain(model.View())
	if strings.Contains(full, "pi working") || strings.Contains(full, "claude stopped") || strings.Contains(full, "3m") {
		t.Fatalf("container row rendered agent details: %q", full)
	}
	if !strings.Contains(full, "● ●") && !strings.Contains(full, "●●") {
		t.Fatalf("container row omitted state dots: %q", full)
	}
	model.SetSize(30, 8)
	narrow := shell.Plain(model.View())
	if !strings.Contains(narrow, "a-very-long…") && !strings.Contains(narrow, "a-very-long-container…") {
		t.Fatalf("narrow row lost name truncation: %q", narrow)
	}
}

func TestRemoteProxyMergesWithRemoteContainerAndRemainsVisibleWhenStale(t *testing.T) {
	model := NewModel(context.Background(), Deps{
		Containers:     []capabilities.Container{{ID: "workspace-1", Name: "api", Machine: "dev", Proxy: true}},
		RemoteMachines: []string{"dev"},
	}, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Backend: "tmux", Places: picker.ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "api", Machine: "dev"}})})
	rows := model.Rows()
	count := 0
	for _, row := range rows {
		if strings.HasPrefix(row, "container:dev:") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("rows = %v, want one merged remote row", rows)
	}
	model.SetSize(100, 12)
	plain := shell.Plain(model.View())
	if strings.Contains(plain, "local proxy") {
		t.Fatalf("view retained removed proxy indicator: %q", plain)
	}
	model = updateModel(t, model, picker.RemoteBatch{Machine: "dev", Error: "offline"})
	count = 0
	for _, row := range model.Rows() {
		if strings.HasPrefix(row, "container:dev:") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("stale rows = %v, want proxy retained after remote failure", model.Rows())
	}
}

func TestRemoteAgentWarningAppearsOnMachineRow(t *testing.T) {
	model := NewModel(context.Background(), Deps{RemoteMachines: []string{"dev"}}, ContainersTab)
	model = updateModel(t, model, picker.RemoteBatch{
		Machine: "dev",
		Places:  picker.ContainerPlaces([]capabilities.Container{{ID: "$1", Name: "api", Machine: "dev"}}),
		Warning: "agents unavailable: nexo: command not found",
	})
	model.SetSize(120, 12)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "agents unavailable: nexo: command not found") {
		t.Fatalf("view = %q, want agent warning on the machine row", plain)
	}
}

func TestContainerOrderingHeadersAndProxyFreeData(t *testing.T) {
	old := time.Now().Add(-12 * time.Minute)
	recent := time.Now().Add(-time.Minute)
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "recent", Name: "recent", Machine: picker.LocalMachine(), Activity: &recent}, {ID: "need", Name: "need", Machine: picker.LocalMachine(), Activity: &old}, {ID: "other", Name: "other", Machine: picker.LocalMachine()}, {ID: "remote", Name: "remote", Machine: "dev.example.local"}}, Agents: []capabilities.Agent{{PaneID: "need", ContainerID: "need", Container: "need", Harness: "pi", State: "stopped", StoppedSince: &old}}}, ContainersTab)
	rows := model.Rows()
	if len(rows) < 4 || rows[1] != "container:"+picker.LocalMachine()+":need" {
		t.Fatalf("rows = %v, want unseen stopped first", rows)
	}
	model.SetSize(100, 12)
	plain := shell.Plain(model.View())
	if !strings.Contains(plain, "dev") || strings.Contains(plain, "dev.example.local") {
		t.Fatalf("machine header was not shortened: %q", plain)
	}
}

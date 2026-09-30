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
		if strings.Contains(line, "one") && !strings.Contains(line, "machine") {
			if strings.Contains(line, "stopped") {
				return
			}
			t.Fatalf("container name and pill were split: %q", line)
		}
	}
	t.Fatal("container row not found")
}

func TestContainerWidthDegradesPillsDotsThenAge(t *testing.T) {
	old := time.Now().Add(-3 * time.Minute)
	model := NewModel(context.Background(), Deps{Containers: []capabilities.Container{{ID: "long", Name: "a-very-long-container-name-that-needs-truncation", Machine: picker.LocalMachine(), Activity: &old}}, Agents: []capabilities.Agent{{PaneID: "a", ContainerID: "long", Container: "long", Harness: "pi", State: "working"}, {PaneID: "b", ContainerID: "long", Container: "long", Harness: "claude", State: "stopped", Seen: true, StoppedSince: &old}}}, ContainersTab)
	model.SetSize(120, 8)
	full := shell.Plain(model.View())
	if !strings.Contains(full, "pi working") || !strings.Contains(full, "claude stopped") || !strings.Contains(full, "3m") {
		t.Fatalf("full container row = %q", full)
	}
	model.SetSize(60, 8)
	medium := shell.Plain(model.View())
	if !strings.Contains(medium, "●●") && !strings.Contains(medium, "● ●") {
		t.Fatalf("medium row did not degrade to dots: %q", medium)
	}
	model.SetSize(30, 8)
	narrow := shell.Plain(model.View())
	if !strings.Contains(narrow, "a-very-long…") && !strings.Contains(narrow, "a-very-long-container…") {
		t.Fatalf("narrow row lost name truncation: %q", narrow)
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

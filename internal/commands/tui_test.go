package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
)

type tuiFakeAdapter struct {
	agents []capabilities.Agent
	popup  string
}

func (f *tuiFakeAdapter) ListAgents(context.Context) ([]capabilities.Agent, error) {
	return f.agents, nil
}

func (f *tuiFakeAdapter) ShowPopup(_ context.Context, command string) error {
	f.popup = command
	return nil
}

func TestTUIRoutesExplicitTabs(t *testing.T) {
	command, ok := NewRegistry().Lookup("tui")
	if !ok {
		t.Fatal("tui command is not registered")
	}
	for _, tab := range []string{"agents", "containers", "projects"} {
		t.Run(tab, func(t *testing.T) {
			adapter := &tuiFakeAdapter{}
			if err := command.Run(context.Background(), Invocation{
				Selection: backend.Selection{Kind: backend.Tmux},
				Adapter:   adapter,
				Args:      []string{tab},
			}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			wantSuffix := " tui '" + tab + "'"
			if tab == "agents" {
				wantSuffix = " tui agents"
			}
			if !strings.HasSuffix(adapter.popup, wantSuffix) {
				t.Fatalf("popup command = %q, want tui %s", adapter.popup, tab)
			}
		})
	}
}

func TestTUIDefaultsToAgentsForUnseenStopped(t *testing.T) {
	adapter := &tuiFakeAdapter{agents: []capabilities.Agent{{State: "stopped"}}}
	command, _ := NewRegistry().Lookup("tui")
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   adapter,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.HasSuffix(adapter.popup, " tui agents") {
		t.Fatalf("popup command = %q, want default agents tab", adapter.popup)
	}
}

func TestTUIDefaultsToContainersWithoutUnseenStopped(t *testing.T) {
	adapter := &tuiFakeAdapter{agents: []capabilities.Agent{{State: "stopped", Seen: true}, {State: "working"}}}
	command, _ := NewRegistry().Lookup("tui")
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   adapter,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.HasSuffix(adapter.popup, " tui 'containers'") {
		t.Fatalf("popup command = %q, want default containers tab", adapter.popup)
	}
}

func TestTUIRejectsUnknownTab(t *testing.T) {
	command, _ := NewRegistry().Lookup("tui")
	err := command.Run(context.Background(), Invocation{Args: []string{"unknown"}})
	if err == nil || !strings.Contains(err.Error(), "unknown tui tab") {
		t.Fatalf("Run() error = %v, want unknown-tab usage error", err)
	}
}

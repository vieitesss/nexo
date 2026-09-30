package commands

import (
	"context"
	"errors"
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

func TestTUIRoutesExplicitTabsWithoutOpeningPopup(t *testing.T) {
	command, ok := NewRegistry().Lookup("tui")
	if !ok {
		t.Fatal("tui command is not registered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tab := range []string{"agents", "containers", "projects"} {
		t.Run(tab, func(t *testing.T) {
			adapter := &tuiFakeAdapter{}
			err := command.Run(ctx, Invocation{
				Selection: backend.Selection{Kind: backend.Tmux},
				Adapter:   adapter,
				Args:      []string{tab},
			})
			if err == nil {
				t.Fatal("Run() error = nil, want canceled picker")
			}
			if adapter.popup != "" {
				t.Fatalf("popup command = %q, want no popup request", adapter.popup)
			}
		})
	}
}

func TestTUIDefaultsToAgentsForUnseenStopped(t *testing.T) {
	adapter := &tuiFakeAdapter{agents: []capabilities.Agent{{State: "stopped"}}}
	if got, want := defaultTab(context.Background(), adapter), "agents"; got != want {
		t.Fatalf("default tab = %q, want %q", got, want)
	}
}

func TestTUIDefaultsToContainersWithoutUnseenStopped(t *testing.T) {
	adapter := &tuiFakeAdapter{agents: []capabilities.Agent{{State: "stopped", Seen: true}, {State: "working"}}}
	if got, want := defaultTab(context.Background(), adapter), "containers"; got != want {
		t.Fatalf("default tab = %q, want %q", got, want)
	}
}

func defaultTab(ctx context.Context, adapter capabilities.AgentSource) string {
	tab, err := tuiTab(ctx, Invocation{Adapter: adapter})
	if err != nil {
		return "error: " + err.Error()
	}
	return tab
}

func TestTUIRejectsUnknownTab(t *testing.T) {
	command, _ := NewRegistry().Lookup("tui")
	err := command.Run(context.Background(), Invocation{Args: []string{"unknown"}})
	if err == nil || !strings.Contains(err.Error(), "unknown tui tab") {
		t.Fatalf("Run() error = %v, want unknown-tab usage error", err)
	}
	var usageErr UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("Run() error = %T, want UsageError", err)
	}
}

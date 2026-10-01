package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/tui"
)

type tuiFakeAdapter struct {
	agents []capabilities.Agent
}

type countingAgentAdapter struct {
	calls int
}

type closeClassificationAdapter struct {
	containers []capabilities.Container
	killed     []string
}

func (a *closeClassificationAdapter) ListContainers(context.Context) ([]capabilities.Container, error) {
	return a.containers, nil
}

func (a *closeClassificationAdapter) KillContainer(_ context.Context, id string) error {
	a.killed = append(a.killed, id)
	return nil
}

type closeActions struct{ killed *[]string }

func (a closeActions) OpenContainer(context.Context, string) error           { return nil }
func (a closeActions) RenameContainer(context.Context, string, string) error { return nil }
func (a closeActions) KillContainer(_ context.Context, id string) error {
	*a.killed = append(*a.killed, id)
	return nil
}

type closeResolver struct{ actions closeActions }

func (r closeResolver) ResolveContainerActions(context.Context, string) (capabilities.ContainerActions, error) {
	return r.actions, nil
}

func (a *countingAgentAdapter) ListAgents(context.Context) ([]capabilities.Agent, error) {
	a.calls++
	return nil, nil
}

func (f *tuiFakeAdapter) ListAgents(context.Context) ([]capabilities.Agent, error) {
	return f.agents, nil
}

func TestTUIRoutesExplicitTabs(t *testing.T) {
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
		})
	}
}

func TestClosingAProxyDoesNotForwardItsLocalID(t *testing.T) {
	adapter := &closeClassificationAdapter{containers: []capabilities.Container{
		{ID: "proxy-id", Machine: "dev", Proxy: true},
		{ID: "remote-id", Machine: "dev"},
	}}
	var remoteKilled []string
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection:         backend.Selection{Kind: backend.Tmux},
		Adapter:           adapter,
		ContainerResolver: closeResolver{actions: closeActions{killed: &remoteKilled}},
		Config:            config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}},
	}, "containers", nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.KillContainers(context.Background(), []picker.ContainerRef{{Machine: "dev", ID: "proxy-id"}, {Machine: "dev", ID: "remote-id"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(adapter.killed, ","); got != "proxy-id" {
		t.Fatalf("local kills = %q, want proxy-id", got)
	}
	if got := strings.Join(remoteKilled, ","); got != "remote-id" {
		t.Fatalf("remote kills = %q, want remote-id only", got)
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

func TestTUIRunsTabbedModelOnRequestedTab(t *testing.T) {
	command, ok := NewRegistry().Lookup("tui")
	if !ok {
		t.Fatal("tui command is not registered")
	}
	original := runTabbedTUI
	defer func() { runTabbedTUI = original }()
	var got []string
	runTabbedTUI = func(_ context.Context, _ tui.Deps, tab tui.Tab) error { got = append(got, string(tab)); return nil }
	for _, tab := range []string{"agents", "containers", "projects"} {
		adapter := &tuiFakeAdapter{}
		if err := command.Run(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter, Args: []string{tab}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(got, ","); got != "agents,containers,projects" {
		t.Fatalf("tabs = %q", got)
	}
}

func TestTUIDependenciesPreserveAnInitialEmptySnapshot(t *testing.T) {
	adapter := &countingAgentAdapter{}
	if _, err := tuiDependencies(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter}, "agents", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 0 {
		t.Fatalf("initial empty snapshot triggered %d duplicate reads", adapter.calls)
	}
	if _, err := tuiDependencies(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter}, "agents", nil, nil, false); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 {
		t.Fatalf("unread snapshot calls = %d, want 1", adapter.calls)
	}
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

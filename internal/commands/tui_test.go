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

func TestTUIWatchesTheResolvedConfigPath(t *testing.T) {
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		ConfigPath: "/tmp/custom-nexo.toml",
	})
	if err != nil {
		t.Fatal(err)
	}
	if deps.ConfigPath != "/tmp/custom-nexo.toml" {
		t.Fatalf("ConfigPath = %q, want the explicit path", deps.ConfigPath)
	}

	deps, err = tuiDependencies(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}})
	if err != nil {
		t.Fatal(err)
	}
	if deps.ConfigPath != config.DefaultPath() {
		t.Fatalf("ConfigPath = %q, want the default path", deps.ConfigPath)
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
	})
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

func TestTUILeavesDefaultTabSelectionToModel(t *testing.T) {
	got, err := tuiTab(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("default tab argument = %q, want empty for model selection", got)
	}
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

func TestTUIDependenciesLeavesAgentReadsToSource(t *testing.T) {
	adapter := &tuiFakeAdapter{}
	deps, err := tuiDependencies(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	if deps.AgentSource != adapter {
		t.Fatalf("AgentSource = %T, want adapter", deps.AgentSource)
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

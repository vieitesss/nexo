package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/tui"
)

type focusTUIAdapter struct{ focused, proxied string }

func (f *focusTUIAdapter) ConfigureAgents(config.AgentsConfig)          {}
func (f *focusTUIAdapter) FocusPane(_ context.Context, id string) error { f.focused = id; return nil }
func (f *focusTUIAdapter) Proxy(_ context.Context, machine, container string) error {
	f.proxied = machine + "/" + container
	return nil
}
func (f *focusTUIAdapter) ListAgents(context.Context) ([]capabilities.Agent, error) { return nil, nil }

func TestTUIFocusRoutesLocalAndRemoteAgentProvenance(t *testing.T) {
	adapter := &focusTUIAdapter{}
	deps, err := tuiDependencies(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter, Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}}}, "agents", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.FocusAgent(context.Background(), tui.AgentRef{Agent: capabilities.Agent{PaneID: "%7"}, Machine: picker.LocalMachine()}); err != nil {
		t.Fatal(err)
	}
	if adapter.focused != "%7" || adapter.proxied != "" {
		t.Fatalf("local focus = %q proxy=%q", adapter.focused, adapter.proxied)
	}
	if err := deps.FocusAgent(context.Background(), tui.AgentRef{Agent: capabilities.Agent{PaneID: "%7", ContainerID: "remote-id"}, Machine: "dev", Backend: string(backend.Tmux)}); err != nil {
		t.Fatal(err)
	}
	if adapter.proxied != "dev/remote-id" || adapter.focused != "%7" {
		t.Fatalf("remote focus proxy=%q pane=%q", adapter.proxied, adapter.focused)
	}
}

func TestTUIRejectsMixedRemoteKinds(t *testing.T) {
	for _, test := range []struct {
		local, remote backend.Kind
	}{
		{local: backend.Tmux, remote: backend.Herdr},
		{local: backend.Herdr, remote: backend.Tmux},
	} {
		t.Run(string(test.local)+"-"+string(test.remote), func(t *testing.T) {
			adapter := &focusTUIAdapter{}
			deps, err := tuiDependencies(context.Background(), Invocation{
				Selection: backend.Selection{Kind: test.local}, Adapter: adapter,
				Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}},
			}, "agents", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = deps.FocusAgent(context.Background(), tui.AgentRef{
				Machine: "dev", Backend: string(test.remote),
				Agent: capabilities.Agent{PaneID: "%7", Container: "api"},
			})
			if err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("error = %v, want explicit mixed-kind rejection", err)
			}
			if adapter.proxied != "" || adapter.focused != "" {
				t.Fatalf("mixed-kind action ran proxy=%q pane=%q", adapter.proxied, adapter.focused)
			}
		})
	}
}

package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/tui"
)

type focusTUIAdapter struct { focused, proxied string }
func (f *focusTUIAdapter) ConfigureAgents(config.AgentsConfig) {}
func (f *focusTUIAdapter) FocusPane(_ context.Context, id string) error { f.focused = id; return nil }
func (f *focusTUIAdapter) Proxy(_ context.Context, machine, container string) error { f.proxied = machine+"/"+container; return nil }
func (f *focusTUIAdapter) ListAgents(context.Context) ([]capabilities.Agent, error) { return nil, nil }

func TestTUIFocusRoutesLocalAndRemoteAgentProvenance(t *testing.T) {
	adapter := &focusTUIAdapter{}
	deps, err := tuiDependencies(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter, Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}}}, "agents", nil, nil)
	if err != nil { t.Fatal(err) }
	if err := deps.FocusAgent(context.Background(), tui.AgentRef{Agent: capabilities.Agent{PaneID: "%7"}, Machine: picker.LocalMachine()}); err != nil { t.Fatal(err) }
	if adapter.focused != "%7" || adapter.proxied != "" { t.Fatalf("local focus = %q proxy=%q", adapter.focused, adapter.proxied) }
	if err := deps.FocusAgent(context.Background(), tui.AgentRef{Agent: capabilities.Agent{PaneID: "%7", ContainerID: "remote-id"}, Machine: "dev", Backend: string(backend.Tmux)}); err != nil { t.Fatal(err) }
	if adapter.proxied != "dev/remote-id" { t.Fatalf("remote focus proxy = %q", adapter.proxied) }
}

package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/tui"
)

// focusTUIAdapter is the smallest adapter shape tuiDependencies needs. proxied
// records the remote kind the proxy seam was asked for, so a test can tell the
// bridge from the native path.
type focusTUIAdapter struct{ focused, proxied string }

func (f *focusTUIAdapter) ConfigureAgents(config.AgentsConfig)          {}
func (f *focusTUIAdapter) FocusPane(_ context.Context, id string) error { f.focused = id; return nil }
func (f *focusTUIAdapter) Proxy(_ context.Context, remoteKind, machine, container string) error {
	f.proxied = remoteKind + ":" + machine + "/" + container
	return nil
}
func (f *focusTUIAdapter) ListAgents(context.Context) ([]capabilities.Agent, error) { return nil, nil }

type focusTUIRunner struct {
	calls   []string
	outputs map[string][]byte
}

func (r *focusTUIRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if output, ok := r.outputs[call]; ok {
		return output, nil
	}
	return nil, nil
}

func TestTUIFocusRoutesLocalAndRemoteAgentProvenance(t *testing.T) {
	adapter := &focusTUIAdapter{}
	runner := &focusTUIRunner{}
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter, Runner: runner,
		Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}, SSHConfig: "/tmp/ssh-config"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.FocusAgent(context.Background(), tui.AgentRef{Agent: capabilities.Agent{PaneID: "%7"}, Machine: containerstate.LocalMachine()}); err != nil {
		t.Fatal(err)
	}
	if adapter.focused != "%7" || adapter.proxied != "" {
		t.Fatalf("local focus = %q proxy=%q", adapter.focused, adapter.proxied)
	}
	if err := deps.FocusAgent(context.Background(), tui.AgentRef{
		Agent:   capabilities.Agent{PaneID: "%7", ContainerID: "remote-id", Container: "api"},
		Machine: "dev", Backend: string(backend.Tmux),
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.proxied != "tmux:dev/api" || adapter.focused != "%7" {
		t.Fatalf("remote focus proxy=%q local pane=%q", adapter.proxied, adapter.focused)
	}
	if got, want := runner.calls, []string{
		"ssh -o BatchMode=yes -o ConnectTimeout=5 -F /tmp/ssh-config dev 'tmux' '-u' 'select-window' '-t' '%7'",
		"ssh -o BatchMode=yes -o ConnectTimeout=5 -F /tmp/ssh-config dev 'tmux' '-u' 'select-pane' '-t' '%7'",
	}; !sameStrings(got, want) {
		t.Fatalf("remote calls = %#v, want %#v", got, want)
	}
}

func TestTUIFocusRoutesTwoRemoteTmuxPanesInOneContainer(t *testing.T) {
	adapter := &focusTUIAdapter{}
	runner := &focusTUIRunner{}
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux}, Adapter: adapter, Runner: runner,
		Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range []string{"%7", "%8"} {
		if err := deps.FocusAgent(context.Background(), tui.AgentRef{
			Agent: capabilities.Agent{PaneID: pane, Container: "api"}, Machine: "dev", Backend: string(backend.Tmux),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if adapter.focused != "" || adapter.proxied != "tmux:dev/api" {
		t.Fatalf("remote focus used local pane=%q proxy=%q", adapter.focused, adapter.proxied)
	}
	if got, want := runner.calls, []string{
		"ssh -o BatchMode=yes -o ConnectTimeout=5 dev 'tmux' '-u' 'select-window' '-t' '%7'",
		"ssh -o BatchMode=yes -o ConnectTimeout=5 dev 'tmux' '-u' 'select-pane' '-t' '%7'",
		"ssh -o BatchMode=yes -o ConnectTimeout=5 dev 'tmux' '-u' 'select-window' '-t' '%8'",
		"ssh -o BatchMode=yes -o ConnectTimeout=5 dev 'tmux' '-u' 'select-pane' '-t' '%8'",
	}; !sameStrings(got, want) {
		t.Fatalf("remote calls = %#v, want %#v", got, want)
	}
}

func TestTUIFocusRoutesTwoRemoteHerdrPanesInOneContainer(t *testing.T) {
	adapter := &focusTUIAdapter{}
	runner := &focusTUIRunner{outputs: map[string][]byte{
		"herdr machine list --json": []byte(`[]`),
	}}
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr}, Adapter: adapter, Runner: runner,
		Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range []string{"remote-workspace:remote-pane-1", "remote-workspace:remote-pane-2"} {
		if err := deps.FocusAgent(context.Background(), tui.AgentRef{
			Agent: capabilities.Agent{PaneID: pane, Container: "api"}, Machine: "dev", Backend: string(backend.Herdr),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if adapter.focused != "" {
		t.Fatalf("remote Herdr focus used local pane=%q", adapter.focused)
	}
	var remoteFocus []string
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "ssh ") {
			remoteFocus = append(remoteFocus, call)
		}
	}
	if got, want := remoteFocus, []string{
		"ssh -o BatchMode=yes -o ConnectTimeout=5 dev 'herdr' 'agent' 'focus' 'remote-workspace:remote-pane-1'",
		"ssh -o BatchMode=yes -o ConnectTimeout=5 dev 'herdr' 'agent' 'focus' 'remote-workspace:remote-pane-2'",
	}; !sameStrings(got, want) {
		t.Fatalf("remote calls = %#v, want %#v", got, want)
	}
}

func TestTUIRejectsUnsupportedRemoteKindAndBridgesToHerdr(t *testing.T) {
	for _, test := range []struct {
		name           string
		local          backend.Kind
		remote         backend.Kind
		wantProxy      string
		wantUnsupporte bool
	}{
		{name: "tmux-herdr", local: backend.Tmux, remote: backend.Herdr, wantProxy: "herdr:dev/api"},
		{name: "rex-herdr", local: backend.Rex, remote: backend.Herdr, wantProxy: "herdr:dev/api"},
		{name: "none-herdr", local: backend.None, remote: backend.Herdr, wantUnsupporte: true},
		{name: "herdr-tmux", local: backend.Herdr, remote: backend.Tmux, wantProxy: "tmux:dev/api"},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := &focusTUIAdapter{}
			deps, err := tuiDependencies(context.Background(), Invocation{
				Selection: backend.Selection{Kind: test.local}, Adapter: adapter, Runner: &focusTUIRunner{},
				Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"dev"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = deps.FocusAgent(context.Background(), tui.AgentRef{
				Machine: "dev", Backend: string(test.remote),
				Agent: capabilities.Agent{PaneID: "%7", Container: "api"},
			})
			if test.wantUnsupporte {
				if err == nil || !strings.Contains(err.Error(), "unsupported") {
					t.Fatalf("error = %v, want unsupported remote kind", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("FocusAgent() error = %v", err)
			}
			if adapter.proxied != test.wantProxy {
				t.Fatalf("proxy = %q, want %q", adapter.proxied, test.wantProxy)
			}
		})
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

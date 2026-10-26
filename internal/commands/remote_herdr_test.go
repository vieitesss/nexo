package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/config"
)

func TestFinishHerdrProxyAttachClosesWorkspaceFromPane(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "pane-1")
	var calls []string
	fake := funcRunner(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if ctx.Err() != nil {
			t.Fatalf("cleanup command context = %v, want fresh context", ctx.Err())
		}
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "herdr" && strings.Join(args, " ") == "pane get pane-1" {
			return []byte(`{"result":{"pane":{"workspace_id":"w1"}}}`), nil
		}
		return nil, nil
	})
	if err := finishHerdrProxyAttach(Invocation{Runner: fake, Adapter: herdr.New(fake)}, nil); err != nil {
		t.Fatalf("finishHerdrProxyAttach() error = %v", err)
	}
	if strings.Join(calls, "|") != "herdr pane get pane-1|herdr workspace close w1" {
		t.Fatalf("calls = %v, want pane lookup and workspace close", calls)
	}
}

func TestRemoteOpenHerdrUsesNativeMachineSupport(t *testing.T) {
	var output strings.Builder
	calls := make([]string, 0)
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "ssh" {
			return []byte("__NEXO_REMOTE_KIND__herdr" + "\n{\"result\":{\"type\":\"workspace_list\",\"workspaces\":[{\"workspace_id\":\"w1\",\"label\":\"api\"}]}}\n"), nil
		}
		if name == "herdr" && strings.Join(args, " ") == "machine list --json" {
			return []byte(`{"machines":[]}`), nil
		}
		return nil, nil
	})
	adapter := herdr.New(fake)
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   adapter,
		Runner:    fake,
		Stdout:    &output,
		Args:      []string{"open", "gpu1/api"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "herdr machine add gpu1 --label gpu1") {
		t.Fatalf("calls = %v, want one native machine add", calls)
	}
	if !strings.Contains(output.String(), "api is available under gpu1") {
		t.Fatalf("output = %q, want native availability message", output.String())
	}
}

func TestRemoteOpenHerdrPreservesNativeContainerSpelling(t *testing.T) {
	var output strings.Builder
	adapter := &recordingRemoteProxy{}
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ssh" {
			return []byte("__NEXO_REMOTE_KIND__herdr\n{\"result\":{\"type\":\"workspace_list\",\"workspaces\":[]}}\n"), nil
		}
		if name == "herdr" && strings.Join(args, " ") == "machine list --json" {
			return []byte(`[{"target":"gpu1","enabled":true}]`), nil
		}
		return nil, nil
	})
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   adapter,
		Runner:    fake,
		Stdout:    &output,
		Args:      []string{"open", "gpu1/app.v1:blue"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "app.v1:blue is available under gpu1") {
		t.Fatalf("output = %q, want native Herdr container spelling", output.String())
	}
	if adapter.opened != [2]string{} {
		t.Fatalf("proxy = %#v, want the native Herdr machine path", adapter.opened)
	}
}

func TestRemoteOpenBridgesTmuxToARemoteHerdrWorkspace(t *testing.T) {
	proxies := &recordingRemoteProxy{}
	fake := herdrWorkspaceListing(
		`{"workspace_id":"w1","label":"web"}`,
		`{"workspace_id":"w2","label":"api"}`,
	)
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   proxies,
		Runner:    fake,
		Args:      []string{"open", "gpu1/api"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The label is a display name; Herdr focuses workspaces by ID.
	if proxies.kind != "herdr" || proxies.opened != [2]string{"gpu1", "w2"} || proxies.calls != 1 {
		t.Fatalf("proxy kind = %q target = %#v calls = %d, want the Herdr proxy for w2", proxies.kind, proxies.opened, proxies.calls)
	}
}

func TestRemoteOpenAcceptsAWorkspaceIDAsTheBridgeTarget(t *testing.T) {
	proxies := &recordingRemoteProxy{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   proxies,
		Runner:    herdrWorkspaceListing(`{"workspace_id":"w2","label":"api"}`),
		Args:      []string{"open", "gpu1/w2"},
	})
	if err != nil || proxies.kind != "herdr" || proxies.opened != [2]string{"gpu1", "w2"} {
		t.Fatalf("error = %v proxy kind = %q target = %#v, want w2 by ID", err, proxies.kind, proxies.opened)
	}
}

func TestRemoteOpenRejectsAnAmbiguousWorkspaceLabelOnTheBridge(t *testing.T) {
	proxies := &recordingRemoteProxy{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   proxies,
		Runner: herdrWorkspaceListing(
			`{"workspace_id":"w1","label":"api"}`,
			`{"workspace_id":"w2","label":"api"}`,
		),
		Args: []string{"open", "gpu1/api"},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "w1, w2") {
		t.Fatalf("error = %v, want an ambiguity diagnostic naming both workspaces", err)
	}
	if proxies.calls != 0 {
		t.Fatalf("proxy = %#v, want no proxy for an ambiguous target", proxies.opened)
	}
}

func TestRemoteOpenReportsABackendThatCannotBridgeHerdr(t *testing.T) {
	proxies := &nonBridgingProxy{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   proxies,
		Runner:    herdrWorkspaceListing(`{"workspace_id":"w2","label":"api"}`),
		Args:      []string{"open", "gpu1/api"},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot attach to a remote Herdr machine") {
		t.Fatalf("error = %v, want the backend's own rejection", err)
	}
	if proxies.calls != 0 {
		t.Fatalf("proxy = %#v, want no tmux proxy for a Herdr remote", proxies.opened)
	}
}

// herdrWorkspaceListing answers every listing with a Herdr kind probe and the
// given workspaces.
func herdrWorkspaceListing(workspaces ...string) funcRunner {
	listing := `{"result":{"type":"workspace_list","workspaces":[` + strings.Join(workspaces, ",") + `]}}`
	return funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ssh" {
			return nil, nil
		}
		return []byte("__NEXO_REMOTE_KIND__herdr\n" + listing + "\n"), nil
	})
}

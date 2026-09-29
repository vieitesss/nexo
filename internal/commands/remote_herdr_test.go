package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

func TestFinishHerdrProxyAttachClosesWorkspaceFromPane(t *testing.T) {
	t.Setenv("HERDR_PANE_ID", "pane-1")
	var calls []string
	fake := runner.Func(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if ctx.Err() != nil {
			t.Fatalf("cleanup command context = %v, want fresh context", ctx.Err())
		}
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "herdr" && strings.Join(args, " ") == "pane get pane-1" {
			return []byte(`{"result":{"pane":{"workspace_id":"w1"}}}`), nil
		}
		return nil, nil
	})
	if err := finishHerdrProxyAttach(Invocation{Runner: fake}, nil); err != nil {
		t.Fatalf("finishHerdrProxyAttach() error = %v", err)
	}
	if strings.Join(calls, "|") != "herdr pane get pane-1|herdr workspace close w1" {
		t.Fatalf("calls = %v, want pane lookup and workspace close", calls)
	}
}

func TestRemoteOpenHerdrUsesNativeMachineSupport(t *testing.T) {
	var output strings.Builder
	calls := make([]string, 0)
	fake := runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
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

func TestHerdrMachineSavedMatchesEnabledSSHTargetOnly(t *testing.T) {
	if !herdrMachineSaved([]byte(`[{"id":"1","label":"other","target":"gpu1","enabled":true}]`), "gpu1") {
		t.Fatal("expected matching target to be recognized")
	}
	if herdrMachineSaved([]byte(`[{"id":"1","label":"gpu1","target":"gpu1","enabled":false}]`), "gpu1") {
		t.Fatal("disabled target must not be recognized")
	}
	if herdrMachineSaved([]byte(`[{"id":"1","label":"gpu1","target":"other","enabled":true}]`), "gpu1") {
		t.Fatal("display label must not identify a machine")
	}
}

func TestRemoteOpenHerdrIsUnsupportedFromTmux(t *testing.T) {
	proxies := &recordingRemoteProxy{}
	fake := runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ssh" {
			t.Fatalf("command = %q, want listing over ssh", name)
		}
		return []byte("__NEXO_REMOTE_KIND__herdr\n{\"result\":{\"type\":\"workspace_list\",\"workspaces\":[]}}\n"), nil
	})
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   proxies,
		Runner:    fake,
		Args:      []string{"open", "gpu1/api"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported") || proxies.opened != [2]string{} {
		t.Fatalf("error = %v, proxy = %#v, want unsupported without proxy", err, proxies.opened)
	}
}

package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/tui"
)

// TestTUIDependenciesRemoteNewUsesResolvedTmuxBackend proves the new-container
// action enters the existing remote New flow with the listing's resolved kind.
// It creates the session and opens the proxy without ever listing the machine
// again, so Enter never probes.
func TestTUIDependenciesRemoteNewUsesResolvedTmuxBackend(t *testing.T) {
	proxy := &recordingRemoteProxy{}
	var commands []string
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return []byte("feature\n"), nil
	})
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   proxy,
		Runner:    fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.NewRemoteContainer(context.Background(), tui.RemoteTarget{Machine: "devbox", Backend: "tmux"}, "feature"); err != nil {
		t.Fatalf("NewRemoteContainer() error = %v", err)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "new-session") || !strings.Contains(commands[0], "'feature'") {
		t.Fatalf("remote commands = %#v, want one tmux new-session", commands)
	}
	if proxy.opened != [2]string{"devbox", "feature"} {
		t.Fatalf("proxy opened = %#v, want devbox/feature", proxy.opened)
	}
}

// TestTUIDependenciesRemoteNewUsesResolvedHerdrBackend covers the native path:
// the resolved Herdr kind creates and opens through the Herdr commands.
func TestTUIDependenciesRemoteNewUsesResolvedHerdrBackend(t *testing.T) {
	var commands []string
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "herdr" {
			return []byte("[]"), nil
		}
		return nil, nil
	})
	deps, err := tuiDependencies(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu"}}},
		Runner:    fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.NewRemoteContainer(context.Background(), tui.RemoteTarget{Machine: "gpu", Backend: "herdr"}, "api"); err != nil {
		t.Fatalf("NewRemoteContainer() error = %v", err)
	}
	if len(commands) == 0 || !strings.Contains(commands[0], "herdr workspace create") {
		t.Fatalf("remote commands = %#v, want a native Herdr workspace create", commands)
	}
}

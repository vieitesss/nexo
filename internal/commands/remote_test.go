package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

func TestRemoteListJSONUsesConfiguredMachines(t *testing.T) {
	var output strings.Builder
	fake := runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ssh" {
			t.Fatalf("runner name = %q, want ssh", name)
		}
		return []byte("$1\tdev\n"), nil
	})
	err := remoteCommand{}.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Runner:    fake,
		Stdout:    &output,
		JSON:      true,
		Args:      []string{"list"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
		t.Fatalf("JSON = %q: %v", output.String(), err)
	}
	if len(got) != 1 || got[0]["machine"] != "devbox" {
		t.Fatalf("results = %#v, want devbox result", got)
	}
}

func TestRemoteListReportsUnreachableMachineWithoutFailing(t *testing.T) {
	var output strings.Builder
	err := remoteCommand{}.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"offline"}}},
		Runner: runner.Func(func(context.Context, string, ...string) ([]byte, error) {
			return nil, context.DeadlineExceeded
		}),
		Stdout: &output,
		Args:   []string{"list"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want per-machine error in output", err)
	}
	if !strings.Contains(output.String(), "offline:") {
		t.Fatalf("output = %q, want offline machine", output.String())
	}
}

func TestRemoteListShowsAgentWarningWithoutHidingContainers(t *testing.T) {
	var output strings.Builder
	err := remoteCommand{}.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Runner: runner.Func(func(context.Context, string, ...string) ([]byte, error) {
			return []byte("$1\tapi\n__NEXO_AGENTS_START__\n\n__NEXO_AGENTS_END__\n__NEXO_AGENTS_WARNING__nexo: command not found\n"), nil
		}),
		Stdout: &output,
		Args:   []string{"list"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want warning without failing the listing", err)
	}
	got := output.String()
	if !strings.Contains(got, "api") || !strings.Contains(got, "warning: agents unavailable: nexo: command not found") {
		t.Fatalf("output = %q, want containers plus agent warning", got)
	}
}

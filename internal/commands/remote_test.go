package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
)

func TestRemoteListJSONUsesConfiguredMachines(t *testing.T) {
	var output strings.Builder
	fake := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
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
		Runner: funcRunner(func(context.Context, string, ...string) ([]byte, error) {
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
		Runner: funcRunner(func(context.Context, string, ...string) ([]byte, error) {
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

func TestRemoteListPassesExpandedSSHConfigToSSH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[remote]\nmachines = [\"devbox\"]\nssh-config = \"~/.ssh/config\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var gotArgs []string
	fake := funcRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("$1\tdev\n"), nil
	})
	var output strings.Builder
	if err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    cfg,
		Runner:    fake,
		Stdout:    &output,
		Args:      []string{"list"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := filepath.Join(home, ".ssh", "config")
	index := slices.Index(gotArgs, "-F")
	if index < 0 || index+1 >= len(gotArgs) || gotArgs[index+1] != want {
		t.Fatalf("ssh args = %#v, want -F %q", gotArgs, want)
	}
}

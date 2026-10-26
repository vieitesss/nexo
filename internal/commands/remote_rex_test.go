package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/proxy"
)

// rexProxyAdapter is the Rex backend's remote surface: a local backend that
// owns one pane per machine, reaches both remote multiplexers through the
// shared attach command, and identifies the proxy the caller sits in.
type rexProxyAdapter struct {
	recordingRemoteProxy
	history []containerstate.Entry
	current string
}

func (a *rexProxyAdapter) RecentContainers(context.Context) ([]containerstate.Entry, error) {
	return a.history, nil
}

func (a *rexProxyAdapter) CurrentProxyMachine(context.Context) (string, error) {
	return a.current, nil
}

// TestRemoteAttachServesTheRexProxyPane pins the command a Rex proxy block runs
// end to end: the attachment reaches the remote client over SSH with Rex's own
// rendezvous, so a proxy pane's explicit switch and its first attachment meet
// on the same remote file.
func TestRemoteAttachServesTheRexProxyPane(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "tmux",
			args: []string{"attach", "--", "reviewbox"},
			want: []string{"nexo-proxy-" + proxy.ProxyRendezvousKey("rex", "reviewbox") + ".tty", "tmux attach-session"},
		},
		{
			name: "herdr",
			args: []string{"attach", "--remote-kind", "herdr", "--initial", "w2", "--", "reviewbox"},
			want: []string{"herdr workspace focus", "exec herdr", "w2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			capture := filepath.Join(dir, "ssh-args")
			t.Setenv("NEXO_TEST_SSH_ARGS", capture)
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$NEXO_TEST_SSH_ARGS\"\nexit 0\n"
			if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			adapter := &rexProxyAdapter{}
			err := (remoteCommand{}).Run(context.Background(), Invocation{
				Selection: backend.Selection{Kind: backend.Rex},
				Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"reviewbox"}, Transport: "ssh"}},
				Adapter:   adapter,
				Args:      test.args,
				Stdin:     strings.NewReader(""),
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			captured, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("read captured ssh argv: %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(string(captured), want) {
					t.Fatalf("attachment argv = %q, want %q", captured, want)
				}
			}
		})
	}
}

// TestRemoteOpenFromRexProxiesTheRemoteContainer pins that a Rex local backend
// can open a remote container: the machine listing resolves the remote kind and
// the Rex adapter creates the proxy.
func TestRemoteOpenFromRexProxiesTheRemoteContainer(t *testing.T) {
	for _, test := range []struct {
		name     string
		runner   funcRunner
		target   string
		wantKind string
		wantOpen [2]string
	}{
		{
			name: "tmux",
			runner: funcRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
				if name != "ssh" {
					return nil, nil
				}
				return []byte("$1\tdev\n"), nil
			}),
			target:   "devbox/dev",
			wantKind: "tmux",
			wantOpen: [2]string{"devbox", "dev"},
		},
		{
			name:     "herdr bridge",
			runner:   herdrWorkspaceListing(`{"workspace_id":"w2","label":"api"}`),
			target:   "devbox/api",
			wantKind: "herdr",
			wantOpen: [2]string{"devbox", "w2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := &rexProxyAdapter{}
			err := (remoteCommand{}).Run(context.Background(), Invocation{
				Selection: backend.Selection{Kind: backend.Rex},
				Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
				Adapter:   adapter,
				Runner:    test.runner,
				Args:      []string{"open", test.target},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if adapter.calls != 1 || adapter.kind != test.wantKind || adapter.opened != test.wantOpen {
				t.Fatalf("proxy kind = %q target = %#v calls = %d, want %q %#v", adapter.kind, adapter.opened, adapter.calls, test.wantKind, test.wantOpen)
			}
		})
	}
}

// TestRemotePreviousFromARexProxyRecreatesTheRecordedKind pins M-o from a Rex
// proxy: the previous container on another machine is reopened with the
// multiplexer its history recorded, so a bridged Herdr remote comes back as a
// Herdr bridge instead of a dead tmux attachment.
func TestRemotePreviousFromARexProxyRecreatesTheRecordedKind(t *testing.T) {
	adapter := &rexProxyAdapter{
		current: "devbox",
		history: []containerstate.Entry{
			{ID: "session:devbox", Machine: "devbox", Proxy: true},
			{ID: "session:rpi", Machine: "rpi", Proxy: true, RemoteKind: "herdr"},
			{ID: "session:web", Machine: "gpu1", Proxy: true},
		},
	}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Rex},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox", "rpi", "gpu1"}}},
		Adapter:   adapter,
		Args:      []string{"previous"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if adapter.calls != 1 || adapter.kind != "herdr" || adapter.opened != [2]string{"rpi", ""} {
		t.Fatalf("proxy kind = %q target = %#v, want the recorded Herdr bridge on rpi", adapter.kind, adapter.opened)
	}
}

var _ capabilities.RemoteProxy = (*rexProxyAdapter)(nil)
var _ capabilities.ContainerHistory = (*rexProxyAdapter)(nil)
var _ capabilities.CurrentProxyMachine = (*rexProxyAdapter)(nil)

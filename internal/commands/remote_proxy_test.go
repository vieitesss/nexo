package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

// recordingRemoteProxy records the remote kind and target of the one proxy call
// a test expects. calls proves a path proxied nothing.
type recordingRemoteProxy struct {
	kind   string
	opened [2]string
	calls  int
}

func (p *recordingRemoteProxy) Proxy(_ context.Context, remoteKind, machine, container string) error {
	p.kind = remoteKind
	p.opened = [2]string{machine, container}
	p.calls++
	return nil
}

// nonBridgingProxy is a backend whose proxy pane reaches its own remote kind
// only, which is what a backend without a foreign multiplexer's client does.
type nonBridgingProxy struct {
	recordingRemoteProxy
}

func (p *nonBridgingProxy) Proxy(ctx context.Context, remoteKind, machine, container string) error {
	if remoteKind != proxy.RemoteKindTmux {
		return errors.New(`backend "rex" cannot attach to a remote Herdr machine`)
	}
	return p.recordingRemoteProxy.Proxy(ctx, remoteKind, machine, container)
}

func TestRemoteOpenRoutesThroughConfiguredRemoteProxy(t *testing.T) {
	proxy := &recordingRemoteProxy{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   proxy,
		Args:      []string{"open", "devbox/$1"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if proxy.opened != [2]string{"devbox", "$1"} {
		t.Fatalf("proxy call = %#v, want devbox/$1", proxy.opened)
	}
}

func TestRemoteOpenCanonicalizesTmuxContainerName(t *testing.T) {
	adapter := &policyBackedProxy{machines: []string{"devbox"}, store: newMemoryProxyStore()}
	invocation := Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Config: config.Config{Remote: config.RemoteConfig{
			Machines: []string{"devbox"},
			Backends: map[string]string{"devbox": "tmux"},
		}},
		Adapter: adapter,
	}

	open := invocation
	open.Args = []string{"open", "devbox/app.v1:blue"}
	if err := (remoteCommand{}).Run(context.Background(), open); err != nil {
		t.Fatalf("open punctuation alias error = %v", err)
	}
	if len(adapter.created) != 1 {
		t.Fatalf("created = %#v, want one workspace for the tmux spelling", adapter.created)
	}

	reopen := invocation
	reopen.Args = []string{"open", "devbox/app_v1_blue"}
	if err := (remoteCommand{}).Run(context.Background(), reopen); err != nil {
		t.Fatalf("reopen canonical name error = %v", err)
	}
	if len(adapter.created) != 1 {
		t.Fatalf("reopen created a duplicate workspace: %#v", adapter.created)
	}

	close := invocation
	close.Runner = funcRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "ssh" {
			t.Fatal("remote close must not probe the remote machine")
		}
		return nil, nil
	})
	close.Args = []string{"close", "devbox/app_v1_blue"}
	if err := (remoteCommand{}).Run(context.Background(), close); err != nil {
		t.Fatalf("close canonical name error = %v", err)
	}
	if len(adapter.killed) != 1 || adapter.killed[0] != adapter.created[0] {
		t.Fatalf("killed = %#v, want the created workspace %#v", adapter.killed, adapter.created)
	}
}

// memoryProxyStore is an in-memory native store behind the shared lookup
// policy.
type memoryProxyStore struct {
	workspaces map[string]string
	order      []string
}

func newMemoryProxyStore() *memoryProxyStore {
	return &memoryProxyStore{workspaces: make(map[string]string)}
}

func (s *memoryProxyStore) ListProxyCandidates(context.Context) ([]proxyidentity.Candidate, error) {
	candidates := make([]proxyidentity.Candidate, 0, len(s.order))
	for _, id := range s.order {
		candidates = append(candidates, proxyidentity.Candidate{ID: id, TaggedIdentity: s.workspaces[id]})
	}
	return candidates, nil
}

func (s *memoryProxyStore) AdoptProxy(_ context.Context, id string, identity proxyidentity.Identity) error {
	s.workspaces[id] = identity.String()
	return nil
}

// policyBackedProxy is a Herdr-like local adapter: it stores whatever identity
// it is given, so the command seam alone is responsible for handing it one
// spelling for a remote tmux container.
type policyBackedProxy struct {
	machines []string
	store    *memoryProxyStore
	created  []string
	killed   []string
}

func (p *policyBackedProxy) Proxy(ctx context.Context, _, machine, container string) error {
	id, err := p.ProxySessionID(ctx, machine, container)
	if err != nil {
		return err
	}
	if id != "" {
		return nil
	}
	id = "w" + strconv.Itoa(len(p.created)+1)
	p.store.order = append(p.store.order, id)
	p.store.workspaces[id] = proxyidentity.NewIdentity(machine, container).String()
	p.created = append(p.created, id)
	return nil
}

func (p *policyBackedProxy) ProxySessionID(ctx context.Context, machine, container string) (string, error) {
	return proxyidentity.Resolve(ctx, proxyidentity.NewIdentity(machine, container), p.store)
}

func (p *policyBackedProxy) KillContainer(_ context.Context, id string) error {
	p.killed = append(p.killed, id)
	return nil
}

type recordingKiller struct {
	closed      string
	proxyTarget [2]string
}

func (k *recordingKiller) KillContainer(_ context.Context, id string) error {
	k.closed = id
	return nil
}

func (*recordingKiller) Proxy(context.Context, string, string, string) error { return nil }

func (k *recordingKiller) ProxySessionID(_ context.Context, machine, container string) (string, error) {
	k.proxyTarget = [2]string{machine, container}
	return "$7", nil
}

func TestRemoteCloseOnlyKillsLocalProxy(t *testing.T) {
	killer := &recordingKiller{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   killer,
		Runner: funcRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ssh" {
				t.Fatal("remote close must not probe SSH")
			}
			return nil, nil
		}),
		Args: []string{"close", "devbox/$1"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if killer.closed != "$7" {
		t.Fatalf("closed target = %q, want stable proxy session id", killer.closed)
	}
}

func TestRemoteCloseLeavesContainerSpellingToTheAdapter(t *testing.T) {
	killer := &recordingKiller{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   killer,
		Runner: funcRunner(func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ssh" {
				t.Fatal("remote close must not probe SSH")
			}
			return nil, nil
		}),
		Args: []string{"close", "devbox/app.v1:blue"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if killer.proxyTarget != [2]string{"devbox", "app.v1:blue"} {
		t.Fatalf("proxy target = %#v, want native spelling", killer.proxyTarget)
	}
}

func TestRemoteNewQuotesRemoteContainerName(t *testing.T) {
	proxy := &recordingRemoteProxy{}
	var got []string
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   proxy,
		Runner: funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "ssh" {
				t.Fatalf("command = %q, want ssh", name)
			}
			got = append([]string(nil), args...)
			return []byte("name with spaces\n"), nil
		}),
		Args: []string{"new", "devbox", "name with spaces"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(got[len(got)-1], "tmux -u new-session -d -P -F '#{session_name}'") || !strings.Contains(got[len(got)-1], "'name with spaces'") {
		t.Fatalf("remote command = %q, want printed canonical name and quoted name", got[len(got)-1])
	}
	if !reflect.DeepEqual(proxy.opened, [2]string{"devbox", "name with spaces"}) {
		t.Fatalf("proxy call = %#v, want new target", proxy.opened)
	}

	got = nil
	err = (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   proxy,
		Runner: funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
			got = append([]string{name}, args...)
			return nil, nil
		}),
		Args: []string{"new", "devbox", "hash#(touch /tmp/nexo-proxy-review)"},
	})
	if err != nil {
		t.Fatalf("Run() with format-looking name error = %v", err)
	}
	if !strings.Contains(got[len(got)-1], "hash##(touch /tmp/nexo-proxy-review)") {
		t.Fatalf("remote command = %q, want escaped tmux format marker", got[len(got)-1])
	}
}

// attachProxyAdapter is the Herdr adapter seam for the hidden attachment: it
// can open proxies and close the workspace that owns a finished one.
type attachProxyAdapter struct {
	closed []string
}

func (a *attachProxyAdapter) Proxy(context.Context, string, string, string) error { return nil }

func (a *attachProxyAdapter) CloseProxyWorkspace(_ context.Context, paneID string) error {
	a.closed = append(a.closed, paneID)
	return nil
}

// TestRemoteAttachUsesTheSelectedBackendRendezvous pins that the process
// transport shared by attachment and explicit switching names the same
// rendezvous file for the backend that runs the attachment.
func TestRemoteAttachUsesTheSelectedBackendRendezvous(t *testing.T) {
	for _, test := range []struct {
		name      string
		selection backend.Selection
		args      []string
		env       map[string]string
	}{
		{name: "tmux", selection: backend.Selection{Kind: backend.Tmux}, args: []string{"attach", "--", "reviewbox"}},
		{
			name:      "herdr",
			selection: backend.Selection{Kind: backend.Herdr},
			args:      []string{"attach", "--from-env"},
			env:       map[string]string{"NEXO_PROXY_MACHINE": "reviewbox", "HERDR_PANE_ID": "p1"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			capture := filepath.Join(dir, "ssh-args")
			t.Setenv("NEXO_TEST_SSH_ARGS", capture)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$NEXO_TEST_SSH_ARGS\"\nexit 0\n"
			if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			adapter := &attachProxyAdapter{}
			test.selection.Adapter = adapter
			err := (remoteCommand{}).Run(context.Background(), Invocation{
				Selection: test.selection,
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
			key := proxy.ProxyRendezvousKey(test.name, "reviewbox")
			if !strings.Contains(string(captured), "nexo-proxy-"+key+".tty") {
				t.Fatalf("attachment argv = %q, want the %s rendezvous %q", captured, test.name, key)
			}
			other := "herdr"
			if test.name == "herdr" {
				other = "tmux"
			}
			if strings.Contains(string(captured), "nexo-proxy-"+proxy.ProxyRendezvousKey(other, "reviewbox")+".tty") {
				t.Fatalf("attachment argv = %q, must not use the %s rendezvous", captured, other)
			}
		})
	}
}

func TestRemoteAttachAcceptsOnlyMachineAndInitialForms(t *testing.T) {
	for _, args := range [][]string{
		{"attach", "--", "machine"},
		{"attach", "machine"}, // the CLI flag parser removes the delimiter
		{"attach", "--initial", "container", "--", "machine"},
		{"attach", "--initial", "container", "machine"},
		{"attach", "--from-env"},
		{"attach", "--initial", "container", "--from-env"},
		{"attach", "--remote-kind", "herdr", "--", "machine"},
		{"attach", "--remote-kind", "tmux", "--initial", "w2", "machine"},
	} {
		if _, err := parseRemoteAttachOptions(args); err != nil {
			t.Errorf("parseRemoteAttachOptions(%#v) = %v, want success", args, err)
		}
	}
	for _, args := range [][]string{
		{"attach", "machine", "container"},
		{"attach", "--initial", "container", "machine", "extra"},
		{"attach", "--from-env", "machine"},
		{"attach", "--remote-kind", "--", "machine"},
		{"attach", "--remote-kind", "zellij", "--", "machine"},
		{"attach", "--remote-kind", "herdr", "--remote-kind", "herdr", "--", "machine"},
	} {
		if _, err := parseRemoteAttachOptions(args); err == nil {
			t.Errorf("parseRemoteAttachOptions(%#v) succeeded, want rejection", args)
		}
	}
}

func TestRemoteAttachValidatesBackendAndMachine(t *testing.T) {
	proxy := &recordingRemoteProxy{}
	for _, test := range []struct {
		name      string
		selection backend.Selection
		want      string
	}{
		{name: "unconfigured machine", selection: backend.Selection{Kind: backend.Tmux}, want: "not configured"},
		{name: "non tmux backend", selection: backend.Selection{Kind: backend.Herdr}, want: "does not support"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (remoteCommand{}).Run(context.Background(), Invocation{
				Selection: test.selection,
				Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
				Adapter:   proxy,
				Args:      []string{"attach", "--", "other"},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

var _ capabilities.RemoteProxy = (*recordingRemoteProxy)(nil)
var _ capabilities.RemoteProxy = (*nonBridgingProxy)(nil)
var _ capabilities.RemoteProxy = (*policyBackedProxy)(nil)
var _ capabilities.ContainerKiller = (*recordingKiller)(nil)
var _ capabilities.ContainerKiller = (*policyBackedProxy)(nil)
var _ proxyidentity.Store = (*memoryProxyStore)(nil)

func TestRemoteAttachBuildsTheHerdrShellForTheBridge(t *testing.T) {
	for _, transport := range []string{"ssh", "mosh", "et"} {
		t.Run(transport, func(t *testing.T) {
			dir := t.TempDir()
			capture := filepath.Join(dir, "ssh-args")
			t.Setenv("NEXO_TEST_SSH_ARGS", capture)
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$NEXO_TEST_SSH_ARGS\"\nexit 0\n"
			if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "mosh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "et"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			adapter := &attachProxyAdapter{}
			err := (remoteCommand{}).Run(context.Background(), Invocation{
				Selection: backend.Selection{Kind: backend.Tmux},
				Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"rpi"}, Transport: transport}},
				Adapter:   adapter,
				Args:      []string{"attach", "--remote-kind", "herdr", "--initial", "w2", "--", "rpi"},
				Stdin:     strings.NewReader(""),
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			captured, err := os.ReadFile(capture)
			if err != nil {
				t.Fatalf("read captured transport argv: %v", err)
			}
			command := string(captured)
			if !strings.Contains(command, "no Herdr server on") || !strings.Contains(command, "workspace focus") || !strings.Contains(command, "exec herdr") {
				t.Fatalf("attachment argv = %q, want the herdr attach shell", command)
			}
			if strings.Contains(command, "nexo-proxy-") || strings.Contains(command, "attach-session") {
				t.Fatalf("attachment argv = %q, want no tmux attachment", command)
			}
		})
	}
}

func TestRemoteAttachRejectsAMisusedRemoteKind(t *testing.T) {
	for _, test := range []struct {
		name      string
		selection backend.Selection
		adapter   capabilities.RemoteProxy
		args      []string
		want      string
	}{
		{
			name: "from-env", selection: backend.Selection{Kind: backend.Herdr}, adapter: &attachProxyAdapter{},
			args: []string{"attach", "--remote-kind", "herdr", "--from-env"}, want: "cannot name a remote Herdr machine",
		},
		{
			name: "unknown kind", selection: backend.Selection{Kind: backend.Tmux}, adapter: &attachProxyAdapter{},
			args: []string{"attach", "--remote-kind", "zellij", "--", "rpi"}, want: "remote-kind tmux|herdr",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (remoteCommand{}).Run(context.Background(), Invocation{
				Selection: test.selection,
				Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"rpi"}}},
				Adapter:   test.adapter,
				Args:      test.args,
				Stdin:     strings.NewReader(""),
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

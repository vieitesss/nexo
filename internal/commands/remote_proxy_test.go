package commands

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/runner"
)

type recordingRemoteProxy struct {
	opened [2]string
}

func (p *recordingRemoteProxy) Proxy(_ context.Context, machine, container string) error {
	p.opened = [2]string{machine, container}
	return nil
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
	close.Runner = runner.Func(func(_ context.Context, name string, _ ...string) ([]byte, error) {
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

func (p *policyBackedProxy) Proxy(ctx context.Context, machine, container string) error {
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
	return proxyidentity.NewPolicy(p.machines).Resolve(ctx, proxyidentity.NewIdentity(machine, container), p.store)
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

func (*recordingKiller) Proxy(context.Context, string, string) error { return nil }

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
		Runner: runner.Func(func(_ context.Context, name string, _ ...string) ([]byte, error) {
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
		Runner: runner.Func(func(_ context.Context, name string, _ ...string) ([]byte, error) {
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
		Runner: runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
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
		Runner: runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
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
				Args:      []string{"attach", "other", "--help"},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

var _ capabilities.RemoteProxy = (*recordingRemoteProxy)(nil)
var _ capabilities.RemoteProxy = (*policyBackedProxy)(nil)
var _ capabilities.ContainerKiller = (*recordingKiller)(nil)
var _ capabilities.ContainerKiller = (*policyBackedProxy)(nil)
var _ proxyidentity.Store = (*memoryProxyStore)(nil)

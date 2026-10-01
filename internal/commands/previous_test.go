package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
)

// historyAdapter is a fake backend that owns container history, live
// containers, and the current identity for one invocation.
type historyAdapter struct {
	entries        []containerstate.Entry
	containers     []capabilities.Container
	current        string
	currentMachine string
	opened         string
	proxied        [2]string
	onOpen         func(id string)
}

func (a *historyAdapter) RecentContainers(context.Context) ([]containerstate.Entry, error) {
	return a.entries, nil
}

func (a *historyAdapter) ListContainers(context.Context) ([]capabilities.Container, error) {
	return a.containers, nil
}

func (a *historyAdapter) CurrentContainerID(context.Context) (string, error) { return a.current, nil }

func (a *historyAdapter) CurrentProxyMachine(context.Context) (string, error) {
	return a.currentMachine, nil
}

func (a *historyAdapter) OpenContainer(_ context.Context, id string) error {
	a.opened = id
	if a.onOpen != nil {
		a.onOpen(id)
	}
	return nil
}

func (a *historyAdapter) Proxy(_ context.Context, machine, container string) error {
	a.proxied = [2]string{machine, container}
	return nil
}

func localEntry(id, name string) containerstate.Entry {
	return containerstate.Entry{ID: id, Name: name, Machine: containerstate.LocalMachine()}
}

func localContainer(id, name string) capabilities.Container {
	return capabilities.Container{ID: id, Name: name, Machine: containerstate.LocalMachine()}
}

func TestPreviousSkipsCurrentAndStaleContainers(t *testing.T) {
	adapter := &historyAdapter{
		entries:    []containerstate.Entry{localEntry("$3", "c"), localEntry("$2", "b"), localEntry("$1", "a")},
		containers: []capabilities.Container{localContainer("$3", "c"), localContainer("$1", "a")},
		current:    "$3",
	}
	if err := (previousCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   adapter,
	}); err != nil {
		t.Fatal(err)
	}
	// $2 is stale (not live) and $3 is current, so the older live $1 wins.
	if adapter.opened != "$1" {
		t.Fatalf("opened = %q, want $1", adapter.opened)
	}
}

func TestPreviousAlternatesBetweenTwoContainers(t *testing.T) {
	a := localEntry("$1", "a")
	b := localEntry("$2", "b")
	adapter := &historyAdapter{
		entries:    []containerstate.Entry{b, a},
		containers: []capabilities.Container{localContainer("$1", "a"), localContainer("$2", "b")},
		current:    "$2",
	}
	adapter.onOpen = func(id string) {
		// Opening a container records the new focus, exactly like the backend.
		entry := a
		if id == "$2" {
			entry = b
		}
		adapter.entries = []containerstate.Entry{entry, a, b}
		adapter.current = id
	}
	run := func() string {
		adapter.opened = ""
		if err := (previousCommand{}).Run(context.Background(), Invocation{
			Selection: backend.Selection{Kind: backend.Tmux},
			Adapter:   adapter,
		}); err != nil {
			t.Fatal(err)
		}
		return adapter.opened
	}
	if got := run(); got != "$1" {
		t.Fatalf("first previous = %q, want $1", got)
	}
	if got := run(); got != "$2" {
		t.Fatalf("second previous = %q, want $2", got)
	}
}

func TestPreviousNoOpsWithoutAnotherLiveContainer(t *testing.T) {
	adapter := &historyAdapter{
		entries:    []containerstate.Entry{localEntry("$1", "only")},
		containers: []capabilities.Container{localContainer("$1", "only")},
		current:    "$1",
	}
	if err := (previousCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   adapter,
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.opened != "" {
		t.Fatalf("opened = %q, want no-op", adapter.opened)
	}
}

func TestPreviousNeverSelectsAProxy(t *testing.T) {
	adapter := &historyAdapter{
		entries:    []containerstate.Entry{{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}, localEntry("$1", "a")},
		containers: []capabilities.Container{{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}, localContainer("$1", "a")},
		current:    "$1",
	}
	if err := (previousCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   adapter,
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.opened != "" {
		t.Fatalf("opened = %q, want no local fallback", adapter.opened)
	}
}

func TestRemotePreviousSkipsCurrentAndUnconfiguredMachines(t *testing.T) {
	adapter := &historyAdapter{
		entries: []containerstate.Entry{
			{ID: "$9", Name: "api", Machine: "devbox", Proxy: true},
			{ID: "$8", Name: "api", Machine: "retired", Proxy: true},
			{ID: "$7", Name: "api", Machine: "buildbox", Proxy: true},
		},
		currentMachine: "devbox",
	}
	if err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox", "buildbox"}}},
		Adapter:   adapter,
		Args:      []string{"previous"},
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.proxied != [2]string{"buildbox", "api"} {
		t.Fatalf("proxy = %#v, want buildbox/api", adapter.proxied)
	}
}

func TestRemotePreviousPropagatesTemporaryConnectionFailure(t *testing.T) {
	adapter := &failingProxyHistory{historyAdapter: historyAdapter{
		entries: []containerstate.Entry{{ID: "$1", Name: "api", Machine: "devbox", Proxy: true}},
	}}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   adapter,
		Args:      []string{"previous"},
	})
	if !errors.Is(err, errTemporaryConnection) {
		t.Fatalf("error = %v, want temporary connection failure", err)
	}
}

var errTemporaryConnection = errors.New("connection timed out")

type failingProxyHistory struct{ historyAdapter }

func (a *failingProxyHistory) Proxy(context.Context, string, string) error {
	return errTemporaryConnection
}

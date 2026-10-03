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

// remoteListingRunner answers every remote listing with the same output, or
// fails every call when err is set.
type remoteListingRunner struct {
	listing string
	err     error
	calls   int
}

func (r *remoteListingRunner) Run(context.Context, string, ...string) ([]byte, error) {
	r.calls++
	return []byte(r.listing), r.err
}

func TestRemotePreviousSkipsContainersGoneFromTheMachine(t *testing.T) {
	adapter := &historyAdapter{
		entries: []containerstate.Entry{
			{ID: "$9", Name: "removed", Machine: "devbox", Proxy: true},
			{ID: "$8", Name: "api", Machine: "devbox", Proxy: true},
		},
	}
	runner := &remoteListingRunner{listing: "$1\tapi\n"}
	if err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   adapter,
		Runner:    runner,
		Args:      []string{"previous"},
	}); err != nil {
		t.Fatal(err)
	}
	// Proxying to a missing tmux session ends the proxy at once, which
	// detaches the client from the local server.
	if adapter.proxied != [2]string{"devbox", "api"} {
		t.Fatalf("proxy = %#v, want devbox/api", adapter.proxied)
	}
	if runner.calls != 1 {
		t.Fatalf("listed devbox %d times, want once", runner.calls)
	}
}

func TestRemotePreviousKeepsEntriesOnUnreachableMachines(t *testing.T) {
	adapter := &historyAdapter{
		entries: []containerstate.Entry{{ID: "$9", Name: "api", Machine: "devbox", Proxy: true}},
	}
	if err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   adapter,
		Runner:    &remoteListingRunner{err: errTemporaryConnection},
		Args:      []string{"previous"},
	}); err != nil {
		t.Fatal(err)
	}
	// The proxy's reconnect loop rides out the outage.
	if adapter.proxied != [2]string{"devbox", "api"} {
		t.Fatalf("proxy = %#v, want devbox/api", adapter.proxied)
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

// displayingHistory is a historyAdapter whose backend can show status-line
// messages, like tmux.
type displayingHistory struct {
	historyAdapter
	messages []string
}

func (a *displayingHistory) DisplayMessage(_ context.Context, message string) error {
	a.messages = append(a.messages, message)
	return nil
}

func runRemotePreviousCommand(t *testing.T, adapter any, machines ...string) error {
	t.Helper()
	return (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: machines}},
		Adapter:   adapter,
		Args:      []string{"previous"},
	})
}

func TestRemotePreviousTogglesBetweenLocalAndProxy(t *testing.T) {
	local := localEntry("$1", "notes")
	proxy := containerstate.Entry{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}
	adapter := &historyAdapter{
		entries:    []containerstate.Entry{local, proxy},
		containers: []capabilities.Container{localContainer("$1", "notes"), {ID: "$2", Name: "devbox/api", Machine: "devbox", Proxy: true}},
		current:    "$1",
	}

	if err := runRemotePreviousCommand(t, adapter, "devbox"); err != nil {
		t.Fatal(err)
	}
	if adapter.proxied != [2]string{"devbox", "api"} {
		t.Fatalf("first remote previous proxied %#v, want devbox/api", adapter.proxied)
	}

	// Inside the proxy, the focus hook has recorded it as the newest entry.
	adapter.entries = []containerstate.Entry{proxy, local}
	adapter.current = "$2"
	adapter.currentMachine = "devbox"
	adapter.proxied = [2]string{}
	if err := runRemotePreviousCommand(t, adapter, "devbox"); err != nil {
		t.Fatal(err)
	}
	if adapter.opened != "$1" || adapter.proxied != [2]string{} {
		t.Fatalf("second remote previous opened %q and proxied %#v, want local $1", adapter.opened, adapter.proxied)
	}
}

func TestRemotePreviousFromProxySkipsDeadLocalContainers(t *testing.T) {
	adapter := &historyAdapter{
		entries: []containerstate.Entry{
			{ID: "$9", Name: "api", Machine: "devbox", Proxy: true},
			localEntry("$3", "gone"),
			localEntry("$1", "notes"),
		},
		containers:     []capabilities.Container{localContainer("$1", "notes")},
		current:        "$9",
		currentMachine: "devbox",
	}
	if err := runRemotePreviousCommand(t, adapter, "devbox"); err != nil {
		t.Fatal(err)
	}
	if adapter.opened != "$1" {
		t.Fatalf("opened = %q, want live local $1", adapter.opened)
	}
}

func TestRemotePreviousFromProxyPicksMostRecentOtherMachine(t *testing.T) {
	remoteFirst := &historyAdapter{
		entries: []containerstate.Entry{
			{ID: "$9", Name: "api", Machine: "devbox", Proxy: true},
			{ID: "$8", Name: "ci", Machine: "buildbox", Proxy: true},
			localEntry("$1", "notes"),
		},
		containers:     []capabilities.Container{localContainer("$1", "notes")},
		currentMachine: "devbox",
	}
	if err := runRemotePreviousCommand(t, remoteFirst, "devbox", "buildbox"); err != nil {
		t.Fatal(err)
	}
	if remoteFirst.proxied != [2]string{"buildbox", "ci"} || remoteFirst.opened != "" {
		t.Fatalf("proxied %#v and opened %q, want buildbox/ci", remoteFirst.proxied, remoteFirst.opened)
	}

	localFirst := &historyAdapter{
		entries: []containerstate.Entry{
			{ID: "$9", Name: "api", Machine: "devbox", Proxy: true},
			localEntry("$1", "notes"),
			{ID: "$8", Name: "ci", Machine: "buildbox", Proxy: true},
		},
		containers:     []capabilities.Container{localContainer("$1", "notes")},
		currentMachine: "devbox",
	}
	if err := runRemotePreviousCommand(t, localFirst, "devbox", "buildbox"); err != nil {
		t.Fatal(err)
	}
	if localFirst.opened != "$1" || localFirst.proxied != [2]string{} {
		t.Fatalf("opened %q and proxied %#v, want local $1", localFirst.opened, localFirst.proxied)
	}
}

func TestRemotePreviousReportsWhenNothingIsEligible(t *testing.T) {
	adapter := &displayingHistory{historyAdapter: historyAdapter{
		entries:    []containerstate.Entry{localEntry("$1", "notes")},
		containers: []capabilities.Container{localContainer("$1", "notes")},
		current:    "$1",
	}}
	if err := runRemotePreviousCommand(t, adapter, "devbox"); err != nil {
		t.Fatalf("error = %v, want the message shown in the multiplexer instead", err)
	}
	if len(adapter.messages) != 1 || adapter.messages[0] != "nexo: no previous container on another machine" {
		t.Fatalf("messages = %#v, want one nothing-eligible message", adapter.messages)
	}
	if adapter.opened != "" || adapter.proxied != [2]string{} {
		t.Fatalf("opened %q and proxied %#v, want no navigation", adapter.opened, adapter.proxied)
	}

	// Without a status line the reason is returned so the CLI prints it.
	plain := &historyAdapter{entries: adapter.entries, containers: adapter.containers, current: "$1"}
	if err := runRemotePreviousCommand(t, plain, "devbox"); err == nil || err.Error() != "no previous container on another machine" {
		t.Fatalf("error = %v, want nothing-eligible error", err)
	}
}

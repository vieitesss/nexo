package rex

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/agentstate"
)

// realIdentity is the whoami reply the identity tests resolve to.
const realIdentity = `{"host_id":"host-real","instance_id":"run-real","session_id":"session:caller","block_id":"block:caller"}`

// resolvedIdentity is one server() outcome.
type resolvedIdentity struct {
	identity identity
	err      error
}

// gatedRunner holds the first Rex request until the test releases it, so a test
// can stand inside a cold backend's whoami. Later requests pass straight
// through, which lets a second capability run while the identity is pending.
type gatedRunner struct {
	inner   *fakeRunner
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	held    bool
}

func newGatedRunner(inner *fakeRunner) *gatedRunner {
	return &gatedRunner{inner: inner, started: make(chan struct{}), release: make(chan struct{})}
}

func (g *gatedRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	g.mu.Lock()
	first := !g.held
	g.held = true
	g.mu.Unlock()
	if first {
		close(g.started)
		<-g.release
	}
	return g.inner.Run(ctx, name, args...)
}

// coldServerCallers starts one server caller, waits until its whoami is in
// flight, starts a second one and releases the gate. The second caller must not
// return while the first whoami is still unresolved.
func coldServerCallers(t *testing.T, backend *Backend, gate *gatedRunner) (resolvedIdentity, resolvedIdentity) {
	t.Helper()
	ctx := context.Background()
	first := make(chan resolvedIdentity, 1)
	go func() {
		resolved, err := backend.server(ctx)
		first <- resolvedIdentity{identity: resolved, err: err}
	}()
	<-gate.started
	second := make(chan resolvedIdentity, 1)
	go func() {
		resolved, err := backend.server(ctx)
		second <- resolvedIdentity{identity: resolved, err: err}
	}()
	select {
	case got := <-second:
		t.Fatalf("second cold caller returned before whoami completed: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}
	close(gate.release)
	return <-first, <-second
}

func TestServerIdentityWaitsForTheOneWhoami(t *testing.T) {
	inner := &fakeRunner{outputs: map[string]string{whoamiKey: realIdentity}}
	gate := newGatedRunner(inner)
	backend := newTestBackend(t, inner, nil)
	backend.Runner = gate

	first, second := coldServerCallers(t, backend, gate)
	want := identity{HostID: "host-real", InstanceID: "run-real", SessionID: "session:caller", BlockID: "block:caller"}
	for name, got := range map[string]resolvedIdentity{"first": first, "second": second} {
		if got.err != nil {
			t.Fatalf("%s server() error = %v", name, got.err)
		}
		if got.identity != want {
			t.Fatalf("%s identity = %#v, want %#v", name, got.identity, want)
		}
	}
	if keys := inner.keys(); len(keys) != 1 {
		t.Fatalf("commands = %#v, want one whoami request", keys)
	}
}

func TestServerIdentitySharesOneFailureWithoutRetry(t *testing.T) {
	cases := []struct {
		name   string
		runner *fakeRunner
		want   string
	}{
		{
			name:   "command failure",
			runner: &fakeRunner{errors: map[string]error{whoamiKey: errors.New("rex is down")}},
			want:   "rex whoami: rex is down",
		},
		{
			name:   "malformed json",
			runner: &fakeRunner{outputs: map[string]string{whoamiKey: `{"host_id":`}},
			want:   "decode rex whoami",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			gate := newGatedRunner(test.runner)
			backend := newTestBackend(t, test.runner, nil)
			backend.Runner = gate

			first, second := coldServerCallers(t, backend, gate)
			for name, got := range map[string]resolvedIdentity{"first": first, "second": second} {
				if got.err == nil || !strings.Contains(got.err.Error(), test.want) {
					t.Fatalf("%s server() error = %v, want %q", name, got.err, test.want)
				}
				if got.identity != (identity{}) {
					t.Fatalf("%s identity = %#v, want no identity without a successful whoami", name, got.identity)
				}
			}
			if first.err.Error() != second.err.Error() {
				t.Fatalf("errors = %q and %q, want one shared failure", first.err, second.err)
			}

			retried, err := backend.server(context.Background())
			if err == nil || err.Error() != first.err.Error() || retried != (identity{}) {
				t.Fatalf("server() = %#v, %v; want the cached failure without a retry", retried, err)
			}
			if keys := test.runner.keys(); len(keys) != 1 {
				t.Fatalf("commands = %#v, want one whoami request", keys)
			}
		})
	}
}

// capabilityOutcome is one capability call's result.
type capabilityOutcome struct {
	name string
	err  error
}

// TestColdCapabilitiesPersistUnderTheResolvedNamespace proves the consequence of
// identity publication: no capability may address durable state before whoami
// completed. Two agent listings batch the same cold adapter the way the TUI
// source does, next to a container listing on the same backend.
func TestColdCapabilitiesPersistUnderTheResolvedNamespace(t *testing.T) {
	inner := &fakeRunner{outputs: map[string]string{
		whoamiKey:               realIdentity,
		api("session.list", ""): sessionsJSON(sessionJSON("session:1", "api")),
		api("session.list_blocks", `{"session_id":"session:1"}`):                          blocksJSON(terminalBlockJSON("block:1", "host-real")),
		api("block.method", blockMethodPayload("session:1", "block:1", "program_status")): statusJSON("blocked", "claude-code", "2026-10-22T09:48:34+02:00"),
		api("block.method", blockMethodPayload("session:1", "block:1", "process")):        `{}`,
		api("session.view", `{"session_id":"session:1"}`): `{"session_id":"session:1","label":"api","active_window_id":"window:1",` +
			`"windows":[{"window_id":"window:1","focused_block_id":"block:1"}]}`,
	}}
	gate := newGatedRunner(inner)
	backend := newTestBackend(t, inner, nil)
	backend.Runner = gate
	ctx := context.Background()

	first := make(chan error, 1)
	go func() {
		_, err := backend.ListAgents(ctx)
		first <- err
	}()
	<-gate.started

	finished := make(chan capabilityOutcome, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := backend.ListContainers(ctx)
		finished <- capabilityOutcome{name: "ListContainers", err: err}
	}()
	go func() {
		defer wg.Done()
		_, err := backend.ListAgents(ctx)
		finished <- capabilityOutcome{name: "ListAgents", err: err}
	}()

	// A cold capability must not finish before the one whoami does.
	var early string
	outcomes := make([]capabilityOutcome, 0, 2)
	select {
	case outcome := <-finished:
		early = outcome.name
		outcomes = append(outcomes, outcome)
	case <-time.After(100 * time.Millisecond):
	}
	close(gate.release)
	wg.Wait()
	for len(outcomes) < 2 {
		outcomes = append(outcomes, <-finished)
	}
	if early != "" {
		t.Fatalf("%s finished before whoami completed", early)
	}
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("%s error = %v", outcome.name, outcome.err)
		}
	}
	if err := <-first; err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}

	resolved := agentstate.New(backend.stateDir, "rex:host-real-run-real", nil)
	if _, err := os.Stat(resolved.Path("json")); err != nil {
		t.Fatalf("agent ledger for the resolved namespace = %v", err)
	}
	entries, err := os.ReadDir(backend.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
		if strings.Contains(entry.Name(), "default") {
			t.Fatalf("state artifacts = %v, want none under the unresolved namespace", names)
		}
	}
}

func TestServerIdentityWithoutACommandRunnerReportsTheMissingRunner(t *testing.T) {
	var backend *Backend
	if _, err := backend.server(context.Background()); err == nil || !strings.Contains(err.Error(), "requires a command runner") {
		t.Fatalf("server() error = %v, want the missing runner", err)
	}
}

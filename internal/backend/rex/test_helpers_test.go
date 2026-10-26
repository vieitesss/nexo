package rex

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

// key is the argument string a test uses to describe one fake response.
func (c call) key() string { return strings.Join(c.args, " ") }

// fakeRunner answers exact Rex command lines and records every request so a
// test can assert the CLI surface a behaviour produces. handler answers a
// request whose payload a test cannot spell in advance, such as a session
// creation carrying the resolved nexo binary; fail injects a request error.
type fakeRunner struct {
	calls   []call
	outputs map[string]string
	errors  map[string]error
	handler func(call) ([]byte, bool)
	fail    func(call) error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	request := call{name: name, args: append([]string(nil), args...)}
	f.calls = append(f.calls, request)
	key := request.key()
	if f.fail != nil {
		if err := f.fail(request); err != nil {
			return nil, err
		}
	}
	if err, ok := f.errors[key]; ok {
		return nil, err
	}
	if output, ok := f.outputs[key]; ok {
		return []byte(output), nil
	}
	if f.handler != nil {
		if output, ok := f.handler(request); ok {
			return output, nil
		}
	}
	return nil, fmt.Errorf("unexpected command: %s %s", name, key)
}

func (f *fakeRunner) keys() []string {
	keys := make([]string, 0, len(f.calls))
	for _, request := range f.calls {
		keys = append(keys, request.key())
	}
	return keys
}

// newTestBackend returns a backend with a deterministic binary and state
// directory so tests never touch the developer's Rex installation or state.
func newTestBackend(t *testing.T, runner *fakeRunner, messages *strings.Builder) *Backend {
	t.Helper()
	backend := New(runner, messages)
	backend.binary = "rex"
	backend.stateDir = t.TempDir()
	return backend
}

// useIdentity skips the whoami request for tests that are not about identity.
func useIdentity(backend *Backend, server identity) {
	backend.identity = server
	backend.identified = true
}

func TestCurrentContainerIDReportsTheCallersSession(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		"--autostart=false whoami --json": `{"host_id":"host-1","instance_id":"instance-1","session_id":"session:caller","block_id":"block:caller"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	got, err := backend.CurrentContainerID(context.Background())
	if err != nil {
		t.Fatalf("CurrentContainerID() error = %v", err)
	}
	if got != "session:caller" {
		t.Fatalf("CurrentContainerID() = %q, want the caller session", got)
	}
	want := []string{"--autostart=false whoami --json"}
	if strings.Join(runner.keys(), "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", runner.keys(), want)
	}
}

func TestCurrentContainerIDIsEmptyOutsideRex(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		"--autostart=false whoami --json": `{"host_id":"host-1","instance_id":"instance-1"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	got, err := backend.CurrentContainerID(context.Background())
	if err != nil || got != "" {
		t.Fatalf("CurrentContainerID() = %q, %v; want no session outside Rex", got, err)
	}
}

func TestServerIdentityIsResolvedOncePerInvocation(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		"--autostart=false whoami --json": `{"host_id":"host-1","instance_id":"instance-1"}`,
	}}
	backend := newTestBackend(t, runner, nil)

	for i := 0; i < 2; i++ {
		if _, err := backend.server(context.Background()); err != nil {
			t.Fatalf("server() error = %v", err)
		}
	}
	if len(runner.calls) != 1 {
		t.Fatalf("commands = %#v, want one whoami request", runner.keys())
	}
}

func TestServerIdentityReportsAnUnreadableServer(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{"--autostart=false whoami --json": `not json`}}
	backend := newTestBackend(t, runner, nil)

	_, err := backend.server(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode rex whoami") {
		t.Fatalf("server() error = %v, want decode failure", err)
	}
}

func TestServerNamespaceScopesStateToTheServerRun(t *testing.T) {
	cases := []struct {
		name     string
		server   identity
		expected string
	}{
		{name: "host and instance", server: identity{HostID: "host-1", InstanceID: "instance-1"}, expected: "host-1-instance-1"},
		{name: "endpoint fallback", server: identity{Endpoint: "unix:///tmp/rex.sock", InstanceID: "instance-1"}, expected: "unix:///tmp/rex.sock-instance-1"},
		{name: "unknown server", server: identity{}, expected: "default"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.server.namespace(); got != test.expected {
				t.Fatalf("namespace() = %q, want %q", got, test.expected)
			}
		})
	}
}

// api is the fake-runner key for one Rex API call, including the safety flag.
func api(method, payload string) string {
	key := "--autostart=false api call " + method
	if payload != "" {
		key += " " + payload
	}
	return key
}

// whoamiKey is the fake-runner key for the server identity probe.
const whoamiKey = "--autostart=false whoami --json"

// connectedApp is one connected desktop app fixture for client.list.
func connectedApp(id, label, connectedAt string) string {
	return `{"client_id":"` + id + `","info":{"kind":"app","label":"` + label + `"},"connected_at":"` + connectedAt + `","connection_state":"connected"}`
}

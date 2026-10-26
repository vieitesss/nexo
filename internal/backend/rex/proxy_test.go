package rex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/statefile"
)

// proxySessionCreate answers the session.create request, whose payload carries
// the resolved nexo binary and therefore cannot be spelled in advance.
func proxySessionCreate(sessionID string) func(call) ([]byte, bool) {
	return func(request call) ([]byte, bool) {
		if len(request.args) == 5 && request.args[2] == "call" && request.args[3] == "session.create" {
			return []byte(`{"session_id":"` + sessionID + `","initial_windows":[{"window_id":"window:1","block_ids":["block:1"]}]}`), true
		}
		return nil, false
	}
}

// createSessionInputs decodes every session.create payload a fake runner saw.
func createSessionInputs(t *testing.T, runner *fakeRunner) []createInput {
	t.Helper()
	var inputs []createInput
	for _, request := range runner.calls {
		if len(request.args) != 5 || request.args[2] != "call" || request.args[3] != "session.create" {
			continue
		}
		var input createInput
		if err := json.Unmarshal([]byte(request.args[4]), &input); err != nil {
			t.Fatalf("decode session.create payload %q: %v", request.args[4], err)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func onlyBlockOptions(t *testing.T, input createInput) createOptions {
	t.Helper()
	if len(input.InitialWindows) != 1 {
		t.Fatalf("session.create windows = %#v, want one terminal block", input.InitialWindows)
	}
	return input.InitialWindows[0].Layout.Block.Options
}

// machineProxyRunner answers the dynamic session.create payload and the fixed
// Rex requests a test names. A `session.list` answer may be sequenced so one
// test can open a proxy and then observe the session it created.
func machineProxyRunner(t *testing.T, sessionID string, outputs map[string]string) *fakeRunner {
	t.Helper()
	runner := &fakeRunner{outputs: map[string]string{api("client.list", ""): `{"clients":[]}`}}
	for key, output := range outputs {
		runner.outputs[key] = output
	}
	runner.handler = func(request call) ([]byte, bool) {
		// The proxy switch is a remote SSH command; its exact shell script is
		// proxy.SwitchSSHArgs' contract, not this adapter's.
		if request.name == "ssh" {
			return nil, true
		}
		return proxySessionCreate(sessionID)(request)
	}
	return runner
}

// newProxyBackend returns a backend whose informational notices are captured
// instead of written to the caller's stream.
func newProxyBackend(t *testing.T, runner *fakeRunner) *Backend {
	t.Helper()
	return newTestBackend(t, runner, &strings.Builder{})
}

// TestProxyOpensOneTmuxProxySessionPerMachine pins the cheapest hybrid: a local
// Rex session whose only block runs the shared hidden attach command for the
// remote tmux, with its machine identity kept out of the mutable session label.
func TestProxyOpensOneTmuxProxySessionPerMachine(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", nil)
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})

	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}

	inputs := createSessionInputs(t, runner)
	if len(inputs) != 1 {
		t.Fatalf("session.create calls = %#v, want one proxy session", runner.keys())
	}
	if inputs[0].Label != "devbox" {
		t.Fatalf("proxy label = %q, want the machine as a display label", inputs[0].Label)
	}
	options := onlyBlockOptions(t, inputs[0])
	if options.Shell != "none" || options.Login == nil || *options.Login {
		t.Fatalf("proxy block options = %#v, want a direct command without a login shell", options)
	}
	command := strings.Join(options.Command, " ")
	if !strings.Contains(command, "--backend rex remote attach") || !strings.Contains(command, "-- 'devbox'") {
		t.Fatalf("proxy block command = %q, want the shared attach command", command)
	}
	if strings.Contains(command, "--initial") {
		t.Fatalf("proxy block command = %q, want an unpinned attachment", command)
	}

	// The identity is durable metadata keyed by the native session id, so a
	// later invocation finds the proxy without reading the label.
	fresh := newTestBackend(t, &fakeRunner{outputs: map[string]string{
		api("session.list", ""): `{"sessions":[{"session_id":"session:proxy","label":"devbox"}]}`,
	}}, &strings.Builder{})
	fresh.stateDir = backend.stateDir
	useIdentity(fresh, identity{HostID: "host-1", InstanceID: "instance-1"})
	sessionID, err := fresh.ProxySessionID(context.Background(), "DEVBOX", "")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if sessionID != "session:proxy" {
		t.Fatalf("ProxySessionID() = %q, want the recorded proxy session", sessionID)
	}
}

// TestProxyFocusesAndRemembersTheProxy pins the user-visible half of opening a
// proxy: the Rex app switches to it, and the machine is recorded as proxy
// history so `remote previous` can come back to it.
func TestProxyFocusesAndRemembersTheProxy(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:proxy"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})

	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if !strings.Contains(strings.Join(runner.keys(), "\n"), `"action":"session.select"`) {
		t.Fatalf("commands = %#v, want the app switched to the proxy session", runner.keys())
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one proxy identity", entries)
	}
	if entry := entries[0]; !entry.Proxy || entry.Machine != "devbox" || entry.ID != "session:proxy" || entry.RemoteKind != "" {
		t.Fatalf("proxy history = %#v, want a devbox tmux proxy", entries[0])
	}
}

// TestProxyBridgesAHerdrRemoteInTheSameMachineProxy pins that remote kind is a
// property of the remote machine, not the local backend: the pane runs the
// Herdr attach shell and the recorded kind recreates the same bridge.
func TestProxyBridgesAHerdrRemoteInTheSameMachineProxy(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", nil)
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})

	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "rpi", "w2"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	command := strings.Join(onlyBlockOptions(t, createSessionInputs(t, runner)[0]).Command, " ")
	if !strings.Contains(command, "--backend rex remote attach --remote-kind herdr --initial 'w2' -- 'rpi'") {
		t.Fatalf("proxy block command = %q, want the Herdr attach command", command)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 1 || !entries[0].Proxy || entries[0].RemoteKind != "herdr" || entries[0].Machine != "rpi" {
		t.Fatalf("proxy history = %#v, want an rpi proxy with the herdr remote kind", entries)
	}
}

// TestProxyReusesTheMachineProxyAndSwitchesItsRemoteTarget pins the one proxy
// per machine invariant: a second explicit open retargets the client in the
// existing pane instead of creating a second proxy session.
func TestProxyReusesTheMachineProxyAndSwitchesItsRemoteTarget(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("session.list", ""): `{"sessions":[{"session_id":"session:proxy","label":"devbox"}]}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	seedProxySession(t, backend)
	seeded := len(createSessionInputs(t, runner))

	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", "api"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if inputs := createSessionInputs(t, runner); len(inputs) != seeded {
		t.Fatalf("session.create calls = %#v, want the existing proxy reused", inputs)
	}
	want := strings.Join(proxy.SwitchSSHArgs(backend.proxyConfig, "rex", proxy.RemoteKindTmux, "devbox", "api"), " ")
	switched := 0
	for _, request := range runner.calls {
		if request.name == "ssh" && request.key() == want {
			switched++
		}
	}
	if switched != 1 {
		t.Fatalf("ssh switches = %d, want one remote client switch", switched)
	}
}

// TestProxySwitchUpdatesTheRememberedRemoteKind pins that a proxy retargeted to
// another remote multiplexer is remembered with the new kind, so M-o recreates
// the bridge that is actually attached.
func TestProxySwitchUpdatesTheRememberedRemoteKind(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("session.list", ""): `{"sessions":[{"session_id":"session:proxy","label":"rpi"}]}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	seedProxySession(t, backend)

	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "devbox", "w2"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RemoteKind != "herdr" || entries[0].Machine != "devbox" {
		t.Fatalf("proxy history = %#v, want the switched herdr bridge", entries)
	}
}

// TestProxySwitchReportsAMissingRemoteTarget pins that an explicit open never
// recreates the proxy when the remote container does not exist, and never
// invents a success message for it.
func TestProxySwitchReportsAMissingRemoteTarget(t *testing.T) {
	missing := exec.Command("sh", "-c", "exit 2").Run()
	if missing == nil {
		t.Fatal("expected a non-zero exit from the helper shell")
	}
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("session.list", ""): `{"sessions":[{"session_id":"session:proxy","label":"devbox"}]}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	seedProxySession(t, backend)
	seeded := len(createSessionInputs(t, runner))
	runner.fail = func(request call) error {
		if request.name == "ssh" {
			return missing
		}
		return nil
	}

	err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", "api")
	if err == nil || !strings.Contains(err.Error(), "no session api on devbox") {
		t.Fatalf("Proxy() error = %v, want a missing remote session message", err)
	}
	if inputs := createSessionInputs(t, runner); len(inputs) != seeded {
		t.Fatalf("session.create calls = %#v, want the healthy proxy left alone", inputs)
	}
}

// TestProxyRejectsAnUnsupportedRemoteKind pins that remote Rex is not a
// terminal attachment: nothing is created for it.
func TestProxyRejectsAnUnsupportedRemoteKind(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", nil)
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})

	err := backend.Proxy(context.Background(), "rex", "devbox", "")
	if err == nil || !strings.Contains(err.Error(), `cannot attach to a remote "rex" machine`) {
		t.Fatalf("Proxy() error = %v, want an unsupported remote kind", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands = %#v, want no native work for an unsupported kind", runner.keys())
	}
}

// TestProxyRequiresAMachine pins that a proxy is never created for an empty or
// unsafe machine name.
func TestProxyRequiresAMachine(t *testing.T) {
	for _, machine := range []string{"", "  ", "-devbox", "dev\nbox"} {
		runner := machineProxyRunner(t, "session:proxy", nil)
		backend := newProxyBackend(t, runner)
		useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
		if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, machine, ""); err == nil {
			t.Fatalf("Proxy(%q) = nil error, want rejection", machine)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("Proxy(%q) commands = %#v, want no native work", machine, runner.keys())
		}
	}
}

// TestProxyRollsBackAnUnidentifiedSession pins that a proxy nexo cannot record
// is destroyed instead of left behind as an unlabeled local session.
func TestProxyRollsBackAnUnidentifiedSession(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", nil)
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	backend.stateDir = t.TempDir()
	document := statefile.Path(backend.stateDir, "rex-metadata-host-1-instance-1", "json")
	if err := os.WriteFile(document, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The identity document stays readable, but nothing can be written to it.
	if err := os.Chmod(backend.stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backend.stateDir, 0o700) })

	err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", "")
	if err == nil {
		t.Fatal("Proxy() = nil error, want the failed identity record reported")
	}
	if !strings.Contains(strings.Join(runner.keys(), "\n"), "session.destroy") {
		t.Fatalf("commands = %#v, want the created proxy rolled back", runner.keys())
	}
}

// TestProxySessionIDIgnoresALabelThatLooksLikeAMachine pins that only nexo's
// sidecar metadata identifies a proxy: a session the user named after a machine
// is a normal session and is never adopted or closed as that machine's proxy.
func TestProxySessionIDIgnoresALabelThatLooksLikeAMachine(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): `{"sessions":[{"session_id":"session:mine","label":"devbox"}]}`,
	}}
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})

	sessionID, err := backend.ProxySessionID(context.Background(), "devbox", "")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if sessionID != "" {
		t.Fatalf("ProxySessionID() = %q, want no proxy for a display label", sessionID)
	}
}

// TestProxySessionIDIgnoresAStaleRecord pins that a proxy record for a session
// the server no longer has resolves to nothing, so close and previous never
// target a dead native id.
func TestProxySessionIDIgnoresAStaleRecord(t *testing.T) {
	runner := machineProxyRunner(t, "session:gone", map[string]string{
		api("session.list", ""): `{"sessions":[]}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	seedProxySession(t, backend)
	// The session is destroyed remotely; only its record remains.
	runner.outputs[api("session.list", "")] = `{"sessions":[]}`

	sessionID, err := backend.ProxySessionID(context.Background(), "devbox", "")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if sessionID != "" {
		t.Fatalf("ProxySessionID() = %q, want the dead proxy ignored", sessionID)
	}
}

// TestListContainersReportsProxySessionsAsMachineProxies pins that a proxy is
// listed as the machine it attaches to, never as a local container, so the TUI
// and `previous` can tell them apart.
func TestListContainersReportsProxySessionsAsMachineProxies(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("session.list", ""):                           `{"sessions":[]}`,
		api("session.view", `{"session_id":"session:2"}`): `{"session_id":"session:2","windows":[]}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "DEVBOX", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	runner.outputs[api("session.list", "")] = `{"sessions":[{"session_id":"session:proxy","label":"devbox"},{"session_id":"session:2","label":"web"}]}`
	runner.outputs[api("session.view", `{"session_id":"session:proxy"}`)] = `{"session_id":"session:proxy","windows":[]}`

	containers, err := backend.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("containers = %#v, want two sessions", containers)
	}
	if container := containers[0]; !container.Proxy || container.Machine != "devbox" || container.Name != "devbox" {
		t.Fatalf("containers[0] = %#v, want the devbox proxy", container)
	}
	if container := containers[1]; container.Proxy || container.Machine != containerstate.LocalMachine() || container.Name != "web" {
		t.Fatalf("containers[1] = %#v, want a local container", container)
	}
}

// TestFocusingAProxySessionKeepsItAProxyInHistory pins that selecting a proxy
// row never demotes it to a local container, which would make `previous` open
// the machine's own proxy as if it were a project.
func TestFocusingAProxySessionKeepsItAProxyInHistory(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("session.list", ""): `{"sessions":[]}`,
		api("client.list", ""):  oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:proxy"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	})
	backend := newProxyBackend(t, runner)
	useIdentity(backend, identity{HostID: "host-1", InstanceID: "instance-1"})
	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "devbox", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	runner.outputs[api("session.list", "")] = `{"sessions":[{"session_id":"session:proxy","label":"devbox"}]}`

	if err := backend.OpenContainer(context.Background(), "session:proxy"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatalf("RecentContainers() error = %v", err)
	}
	if len(entries) != 1 || !entries[0].Proxy || entries[0].Machine != "devbox" || entries[0].RemoteKind != "herdr" {
		t.Fatalf("history = %#v, want the proxy identity preserved", entries)
	}
}

// TestCurrentProxyMachineReportsTheCallersProxyMachine pins M-o's starting
// point: inside a proxy session the current machine is the proxied one.
func TestCurrentProxyMachineReportsTheCallersProxyMachine(t *testing.T) {
	runner := machineProxyRunner(t, "session:proxy", map[string]string{
		api("session.list", ""): `{"sessions":[]}`,
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1","session_id":"session:proxy"}`,
	})
	backend := newProxyBackend(t, runner)
	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "DevBox", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}

	machine, err := backend.CurrentProxyMachine(context.Background())
	if err != nil {
		t.Fatalf("CurrentProxyMachine() error = %v", err)
	}
	if machine != "devbox" {
		t.Fatalf("CurrentProxyMachine() = %q, want the proxied machine", machine)
	}
}

// TestCurrentProxyMachineIsEmptyOutsideAProxy pins that an ordinary session is
// not on some remote machine.
func TestCurrentProxyMachineIsEmptyOutsideAProxy(t *testing.T) {
	runner := &fakeRunner{outputs: map[string]string{
		whoamiKey: `{"host_id":"host-1","instance_id":"instance-1","session_id":"session:2"}`,
	}}
	backend := newProxyBackend(t, runner)

	machine, err := backend.CurrentProxyMachine(context.Background())
	if err != nil {
		t.Fatalf("CurrentProxyMachine() error = %v", err)
	}
	if machine != "" {
		t.Fatalf("CurrentProxyMachine() = %q, want no remote machine", machine)
	}
}

// TestProxyMetadataIsScopedToTheServerRun pins that native session ids are only
// trusted inside the server run that issued them.
func TestProxyMetadataIsScopedToTheServerRun(t *testing.T) {
	shared := t.TempDir()
	first := newProxyBackend(t, machineProxyRunner(t, "session:proxy", nil))
	first.stateDir = shared
	useIdentity(first, identity{HostID: "host-1", InstanceID: "instance-1"})
	if err := first.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}

	other := newTestBackend(t, &fakeRunner{outputs: map[string]string{
		api("session.list", ""): `{"sessions":[{"session_id":"session:proxy","label":"devbox"}]}`,
	}}, &strings.Builder{})
	other.stateDir = shared
	useIdentity(other, identity{HostID: "host-1", InstanceID: "instance-2"})
	sessionID, err := other.ProxySessionID(context.Background(), "devbox", "")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if sessionID != "" {
		t.Fatalf("ProxySessionID() = %q, want another server run's ids ignored", sessionID)
	}
}

// seedProxySession opens the machine proxy a test then reuses.
func seedProxySession(t *testing.T, backend *Backend) {
	t.Helper()
	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", ""); err != nil {
		t.Fatalf("seed proxy error = %v", err)
	}
}

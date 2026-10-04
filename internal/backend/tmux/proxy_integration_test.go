package tmux

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/runner"
)

type isolatedTmuxRunner struct {
	socket string
}

func (r isolatedTmuxRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "tmux" {
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}
	args = append([]string(nil), args...)
	operation := args[0]
	if operation == "-u" && len(args) > 1 {
		operation = args[1]
	}
	if operation == "new-session" {
		// Keep this integration test from launching the generated hidden attach
		// command; the session-name parser is the behavior under test.
		args[len(args)-1] = "sleep 600"
	}
	return runner.ExecRunner{}.Run(ctx, "tmux", append([]string{"-L", r.socket, "-f", "/dev/null"}, args...)...)
}

func (r isolatedTmuxRunner) RunInteractive(context.Context, string, ...string) error {
	return nil
}

type realProxyTmuxRunner struct {
	binary string
	socket string
}

func (r realProxyTmuxRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "tmux" {
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}
	args = append([]string(nil), args...)
	binary := r.binary
	if binary == "" {
		binary = "tmux"
	}
	return runner.ExecRunner{}.Run(ctx, binary, append([]string{"-L", r.socket, "-f", "/dev/null"}, args...)...)
}

func (r realProxyTmuxRunner) RunInteractive(context.Context, string, ...string) error {
	return nil
}

func realTmuxPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is unavailable")
	}
	return path
}

func runPrivateTmux(t *testing.T, tmuxPath, socket string, args ...string) []byte {
	t.Helper()
	command := exec.Command(tmuxPath, append([]string{"-L", socket, "-f", "/dev/null"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v (%s)", args, err, output)
	}
	return output
}

func TestMachineProxyDoesNotAdoptLegacyOrLocalNameOnThrowawayTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	socket := "nexo-test-adopt-" + strings.ReplaceAll(t.Name(), "/", "-")
	r := isolatedTmuxRunner{socket: socket}
	ctx := context.Background()
	t.Cleanup(func() { _, _ = r.Run(ctx, "tmux", "kill-server") })
	for _, name := range []string{"vieitesrpi", "vieitesrpi/foo", "notamachine/foo"} {
		if _, err := r.Run(ctx, "tmux", "new-session", "-d", "-s", name, "sleep 600"); err != nil {
			t.Fatalf("create session %q: %v", name, err)
		}
	}
	backend := New(r, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{" VIEITESRPI "}}, "")
	containers, err := backend.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers(): %v", err)
	}
	var local capabilities.Container
	for _, container := range containers {
		if container.Name == "vieitesrpi" && !container.Proxy {
			local = container
		}
		if container.Proxy {
			t.Fatalf("containers = %#v, want untagged sessions never classified as proxies", containers)
		}
	}
	if local.ID == "" {
		t.Fatalf("containers = %#v, want local same-name session", containers)
	}
	if err := backend.Proxy(ctx, "vieitesrpi", "foo"); err != nil {
		t.Fatalf("create machine proxy: %v", err)
	}
	output, err := r.Run(ctx, "tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{@nexo-proxy}")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if !strings.Contains(string(output), "\tvieitesrpi~remote\tvieitesrpi") {
		t.Fatalf("sessions = %q, want suffixed machine proxy with machine tag", output)
	}
	if value, ok := showTmuxOption(ctx, t, r, false, local.ID, "@nexo-proxy"); ok && value != "" {
		t.Fatalf("local same-name session tag = %q, want unset", value)
	}
}

// showTmuxOption reads an option without tmux's fallback semantics: with
// global it reads the global table, otherwise the target's session-local
// value. ok is false when the option is unset.
func showTmuxOption(ctx context.Context, t *testing.T, r isolatedTmuxRunner, global bool, target, option string) (string, bool) {
	t.Helper()
	args := []string{"show-options", "-t", target, "-v", option}
	if global {
		args = []string{"show-options", "-gv", option}
	}
	output, err := r.Run(ctx, "tmux", args...)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
}

func writeRealTransportWrappers(t *testing.T, tmuxPath, socket string) string {
	t.Helper()
	dir := t.TempDir()
	sshScript := `#!/bin/sh
cmd=
for arg do cmd=$arg; done
if [ "${NEXO_TEST_EMPTY:-}" = 1 ]; then
  (sleep 0.3; printf q > /dev/tty) &
fi
exec sh -c "$cmd"
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(sshScript), 0o755); err != nil {
		t.Fatal(err)
	}
	tmuxScript := "#!/bin/sh\nexec " + tmuxPath + " -L " + socket + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(tmuxScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func scriptCommand(args []string) *exec.Cmd {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = proxy.ShellQuote(arg)
	}
	command := exec.Command("script", "-q", "-c", strings.Join(quoted, " "), "/dev/null")
	command.Env = terminalEnv()
	return command
}

// terminalEnv gives a scripted tmux client the terminal a real SSH login
// carries. CI runs without TERM, and tmux 3.4 refuses to attach without one
// ("open terminal failed: terminal does not support clear"); TMUX is dropped
// so a run inside tmux does not count as nesting.
func terminalEnv() []string {
	env := []string{"TERM=xterm-256color"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TERM=") && !strings.HasPrefix(entry, "TMUX=") {
			env = append(env, entry)
		}
	}
	return env
}

// startScripted runs command in the background and returns a function that
// waits for it to exit, up to timeout, and returns its combined output.
func startScripted(command *exec.Cmd) (<-chan error, func(time.Duration) string) {
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	result := make(chan error, 1)
	go func() { result <- command.Run() }()
	exited := func(timeout time.Duration) string {
		select {
		case err := <-result:
			result <- err
			return output.String()
		case <-time.After(timeout):
			return "(still running)"
		}
	}
	return result, exited
}

func attachAndDetachRemote(t *testing.T, tmuxPath, socket, target string) {
	t.Helper()
	command := scriptCommand([]string{tmuxPath, "-L", socket, "-f", "/dev/null", "attach-session", "-d", "-t", "=" + target})
	result, output := startScripted(command)
	clientPID := waitForRemoteClient(tmuxPath, socket)
	if clientPID == "" {
		t.Fatalf("remote attach did not create a client; output: %q", output(time.Second))
	}
	if err := exec.Command("kill", clientPID).Run(); err != nil {
		t.Fatalf("stop remote client %s: %v", clientPID, err)
	}
	if err := <-result; err != nil {
		t.Fatalf("attach remote session %s: %v", target, err)
	}
}

func waitForRemoteClient(tmuxPath, socket string) string {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		output, err := exec.Command(tmuxPath, "-L", socket, "-f", "/dev/null", "list-clients", "-F", "#{client_pid}").CombinedOutput()
		if err == nil {
			if value := strings.TrimSpace(string(output)); value != "" {
				return strings.Fields(value)[0]
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

func runTransportAndDetach(t *testing.T, args []string, tmuxPath, socket string) {
	t.Helper()
	command := scriptCommand(args)
	result, output := startScripted(command)
	clientPID := waitForRemoteClient(tmuxPath, socket)
	if clientPID == "" {
		t.Fatalf("remote transport did not create a client; output: %q", output(time.Second))
	}
	if err := exec.Command("kill", clientPID).Run(); err != nil {
		t.Fatalf("stop remote client %s: %v", clientPID, err)
	}
	if err := <-result; err != nil {
		t.Fatalf("remote transport attach: %v", err)
	}
}

func sessionLastAttached(t *testing.T, tmuxPath, socket string) map[string]int64 {
	t.Helper()
	output := runPrivateTmux(t, tmuxPath, socket, "list-sessions", "-F", "#{session_name} #{session_last_attached}")
	result := make(map[string]int64)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("session_last_attached %q: %v", line, err)
		}
		result[fields[0]] = value
	}
	return result
}

func TestUnpinnedTransportUsesMostRecentlyAttachedRemoteSession(t *testing.T) {
	tmuxPath := realTmuxPath(t)
	socket := "nexo-test-remote-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	runPrivateTmux(t, tmuxPath, socket, "new-session", "-d", "-s", "first", "sleep", "600")
	runPrivateTmux(t, tmuxPath, socket, "new-session", "-d", "-s", "second", "sleep", "600")
	t.Cleanup(func() { _, _ = exec.Command(tmuxPath, "-L", socket, "kill-server").CombinedOutput() })
	attachAndDetachRemote(t, tmuxPath, socket, "first")
	before := sessionLastAttached(t, tmuxPath, socket)
	if before["first"] <= before["second"] {
		t.Fatalf("session_last_attached before unpinned attach = %#v, want first newest", before)
	}
	wrapperDir := writeRealTransportWrappers(t, tmuxPath, socket)
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	args := transportArgs(config.RemoteConfig{Transport: "ssh"}, "fake-machine", "")
	runTransportAndDetach(t, args, tmuxPath, socket)
	after := sessionLastAttached(t, tmuxPath, socket)
	if after["first"] <= after["second"] {
		t.Fatalf("session_last_attached after unpinned attach = %#v, want first selected", after)
	}
}

func TestEmptyRemoteTransportWaitsForQuit(t *testing.T) {
	tmuxPath := realTmuxPath(t)
	socket := "nexo-test-empty-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { _, _ = exec.Command(tmuxPath, "-L", socket, "kill-server").CombinedOutput() })
	wrapperDir := writeRealTransportWrappers(t, tmuxPath, socket)
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	args := transportArgs(config.RemoteConfig{Transport: "ssh"}, "fake-machine", "")
	started := time.Now()
	command := scriptCommand(args)
	input, inputWriter, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	defer func() { _ = input.Close(); _ = inputWriter.Close() }()
	command.Stdin = input
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = inputWriter.Write([]byte{'q'})
	}()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("empty remote attach: %v (%s)", err, output)
	}
	if time.Since(started) < 250*time.Millisecond {
		t.Logf("empty remote output before early exit: %q", output)
		t.Fatalf("empty remote exited before prompt could remain visible")
	}
	if !strings.Contains(string(output), "no sessions on fake-machine — press r to retry, q to close") {
		t.Fatalf("empty remote output = %q, want retry/quit prompt", output)
	}
}

func buildNexoForProxyTest(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	binary := filepath.Join(t.TempDir(), "nexo")
	command := exec.Command("go", "build", "-o", binary, "./cmd/nexo")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build nexo proxy binary: %v (%s)", err, output)
	}
	return binary
}

func waitForProxyValue(t *testing.T, timeout time.Duration, read func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if value := read(); value == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q", want)
}

func waitForProxyPID(t *testing.T, timeout time.Duration, read func() string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if value := read(); value != "" {
			return value
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for proxy pane pid")
	return ""
}

func remoteClientSession(tmuxPath, socket string) string {
	output, err := exec.Command(tmuxPath, "-L", socket, "-f", "/dev/null", "list-clients", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func TestMachineProxyRealTransportSwitchesWithoutRespawnAndReconnects(t *testing.T) {
	tmuxPath := realTmuxPath(t)
	remoteSocket := "nexo-test-e2e-remote-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	localSocket := "nexo-test-e2e-local-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	runPrivateTmux(t, tmuxPath, remoteSocket, "new-session", "-d", "-s", "first", "sleep", "600")
	runPrivateTmux(t, tmuxPath, remoteSocket, "new-session", "-d", "-s", "second", "sleep", "600")
	t.Cleanup(func() {
		_, _ = exec.Command(tmuxPath, "-L", localSocket, "kill-server").CombinedOutput()
		_, _ = exec.Command(tmuxPath, "-L", remoteSocket, "kill-server").CombinedOutput()
	})
	wrapperDir := writeRealTransportWrappers(t, tmuxPath, remoteSocket)
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[remote]\nmachines = [\"fake-machine\"]\ntransport = \"ssh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := buildNexoForProxyTest(t)
	oldArg := os.Args[0]
	os.Args[0] = binary
	t.Cleanup(func() { os.Args[0] = oldArg })
	runner := realProxyTmuxRunner{binary: tmuxPath, socket: localSocket}
	backend := New(runner, false)
	backend.usageStateDir = t.TempDir()
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"fake-machine"}, Transport: "ssh"}, configPath)
	ctx := context.Background()
	if err := backend.Proxy(ctx, "fake-machine", "first"); err != nil {
		t.Fatalf("initial explicit proxy: %v", err)
	}
	waitForProxyValue(t, 3*time.Second, func() string { return remoteClientSession(tmuxPath, remoteSocket) }, "first")
	proxyID, err := backend.ProxySessionID(ctx, "fake-machine", "")
	if err != nil || proxyID == "" {
		t.Fatalf("ProxySessionID() = %q, %v", proxyID, err)
	}
	panePID := func() string {
		output, _ := runner.Run(ctx, "tmux", "display-message", "-p", "-t", proxyID, "#{pane_pid}")
		return strings.TrimSpace(string(output))
	}
	initialPID := waitForProxyPID(t, 3*time.Second, panePID)

	if err := backend.Proxy(ctx, "fake-machine", "second"); err != nil {
		t.Fatalf("explicit open on existing proxy: %v", err)
	}
	waitForProxyValue(t, 3*time.Second, func() string { return remoteClientSession(tmuxPath, remoteSocket) }, "second")
	if got := panePID(); got != initialPID {
		t.Fatalf("pane pid after client switch = %q, want unchanged %q", got, initialPID)
	}
	if err := backend.Proxy(ctx, "fake-machine", ""); err != nil {
		t.Fatalf("M-o first reuse: %v", err)
	}
	if err := backend.Proxy(ctx, "fake-machine", ""); err != nil {
		t.Fatalf("M-o second reuse: %v", err)
	}
	if got := remoteClientSession(tmuxPath, remoteSocket); got != "second" {
		t.Fatalf("remote session after repeated M-o = %q, want second", got)
	}
	if err := backend.Proxy(ctx, "fake-machine", "missing"); err == nil || !strings.Contains(err.Error(), "no session missing on fake-machine") {
		t.Fatalf("explicit missing open error = %v, want clear missing-session error", err)
	}
	if got := panePID(); got != initialPID {
		t.Fatalf("pane pid after missing explicit open = %q, want unchanged %q", got, initialPID)
	}

	pidPath := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "nexo-proxy-"+proxy.ProxyRendezvousKey("tmux", "fake-machine")+".tty")
	if err := os.Remove(pidPath); err != nil {
		t.Fatalf("remove stale proxy pid: %v", err)
	}
	if err := backend.Proxy(ctx, "fake-machine", "first"); err != nil {
		t.Fatalf("explicit open fallback: %v", err)
	}
	waitForProxyValue(t, 3*time.Second, func() string { return remoteClientSession(tmuxPath, remoteSocket) }, "first")
	if got := panePID(); got == initialPID || got == "" {
		t.Fatalf("pane pid after stale-pid fallback = %q, want a respawned pane", got)
	}
}

func TestConflictingProxyTagIsNeverAdoptedOnThrowawayTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	socket := "nexo-test-conflict-" + strings.ReplaceAll(t.Name(), "/", "-")
	r := isolatedTmuxRunner{socket: socket}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.Run(ctx, "tmux", "kill-server")
	})
	if _, err := r.Run(ctx, "tmux", "new-session", "-d", "-s", "vieitesrpi/foo", "sleep 600"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := r.Run(ctx, "tmux", "set-option", "-t", "vieitesrpi/foo", "@nexo-proxy", "vieitesrpi/bar"); err != nil {
		t.Fatalf("tag session: %v", err)
	}
	backend := New(r, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"vieitesrpi"}}, "")

	id, err := backend.ProxySessionID(ctx, "vieitesrpi", "foo")
	if err != nil {
		t.Fatalf("ProxySessionID(vieitesrpi, foo): %v", err)
	}
	if id != "" {
		t.Fatalf("ProxySessionID(vieitesrpi, foo) = %q, want no adoption of a conflicting tag", id)
	}
	if value, ok := showTmuxOption(ctx, t, r, false, "vieitesrpi/foo", "@nexo-proxy"); !ok || value != "vieitesrpi/bar" {
		t.Fatalf("tag = %q (set=%v), want vieitesrpi/bar: the tag must not be overwritten", value, ok)
	}
	// A tag naming another identity wins over the display name when listing.
	containers, err := backend.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers(): %v", err)
	}
	if len(containers) != 1 || !containers[0].Proxy || containers[0].Machine != "vieitesrpi" || containers[0].Name != "bar" {
		t.Fatalf("containers = %#v, want the conflicting tag classified as vieitesrpi/bar", containers)
	}
	// The tag remains reachable under the identity it names.
	tagged, err := backend.ProxySessionID(ctx, "vieitesrpi", "bar")
	if err != nil {
		t.Fatalf("ProxySessionID(vieitesrpi, bar): %v", err)
	}
	if tagged == "" {
		t.Fatal("ProxySessionID(vieitesrpi, bar) = \"\", want the tagged session")
	}
}

func TestProxyUsesOneMachineMetadataOnThrowawayTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	socket := "nexo-test-proxy-" + strings.ReplaceAll(t.Name(), "/", "-")
	r := isolatedTmuxRunner{socket: socket}
	ctx := context.Background()
	t.Cleanup(func() { _, _ = r.Run(ctx, "tmux", "kill-server") })

	backend := New(r, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	if err := backend.Proxy(ctx, "devbox", "prod"); err != nil {
		t.Fatalf("create machine proxy: %v", err)
	}
	if err := backend.Proxy(ctx, "devbox", ""); err != nil {
		t.Fatalf("reopen machine proxy: %v", err)
	}
	if _, err := backend.ProxySessionID(ctx, "devbox", ""); err != nil {
		t.Fatalf("find machine proxy: %v", err)
	}
	if err := backend.Proxy(ctx, "devbox", "unsafe;"); err == nil {
		t.Fatal("Proxy() accepted a trailing semicolon")
	}

	output, err := r.Run(ctx, "tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{@nexo-proxy}")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 1 || !strings.Contains(string(output), "\tdevbox\tdevbox") {
		t.Fatalf("sessions = %q, want one machine proxy", output)
	}
}

func TestMachineProxyRemoteSessionRemovalKeepsPaneAndResolvesNextOrPrompt(t *testing.T) {
	tmuxPath := realTmuxPath(t)
	remoteSocket := "nexo-test-remove-remote-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	localSocket := "nexo-test-remove-local-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	runPrivateTmux(t, tmuxPath, remoteSocket, "new-session", "-d", "-s", "first", "sleep", "600")
	runPrivateTmux(t, tmuxPath, remoteSocket, "new-session", "-d", "-s", "second", "sleep", "600")
	t.Cleanup(func() {
		_, _ = exec.Command(tmuxPath, "-L", localSocket, "kill-server").CombinedOutput()
		_, _ = exec.Command(tmuxPath, "-L", remoteSocket, "kill-server").CombinedOutput()
	})
	wrapperDir := writeRealTransportWrappers(t, tmuxPath, remoteSocket)
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[remote]\nmachines = [\"fake-machine\"]\ntransport = \"ssh\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := buildNexoForProxyTest(t)
	oldArg := os.Args[0]
	os.Args[0] = binary
	t.Cleanup(func() { os.Args[0] = oldArg })
	runner := realProxyTmuxRunner{binary: tmuxPath, socket: localSocket}
	backend := New(runner, false)
	backend.usageStateDir = t.TempDir()
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"fake-machine"}, Transport: "ssh"}, configPath)
	ctx := context.Background()
	if err := backend.Proxy(ctx, "fake-machine", "first"); err != nil {
		t.Fatalf("initial explicit proxy: %v", err)
	}
	waitForProxyValue(t, 3*time.Second, func() string { return remoteClientSession(tmuxPath, remoteSocket) }, "first")
	proxyID, err := backend.ProxySessionID(ctx, "fake-machine", "")
	if err != nil || proxyID == "" {
		t.Fatalf("ProxySessionID() = %q, %v", proxyID, err)
	}
	panePID := func() string {
		output, _ := runner.Run(ctx, "tmux", "display-message", "-p", "-t", proxyID, "#{pane_pid}")
		return strings.TrimSpace(string(output))
	}
	initialPID := waitForProxyPID(t, 3*time.Second, panePID)
	if err := backend.Proxy(ctx, "fake-machine", "second"); err != nil {
		t.Fatalf("switch to second: %v", err)
	}
	waitForProxyValue(t, 3*time.Second, func() string { return remoteClientSession(tmuxPath, remoteSocket) }, "second")

	if _, err := exec.Command(tmuxPath, "-L", remoteSocket, "kill-session", "-t", "=second").CombinedOutput(); err != nil {
		t.Fatalf("kill shown remote session: %v", err)
	}
	waitForProxyValue(t, 3*time.Second, func() string { return remoteClientSession(tmuxPath, remoteSocket) }, "first")
	if got := panePID(); got != initialPID {
		t.Fatalf("pane pid after shown session removal = %q, want unchanged %q", got, initialPID)
	}

	if _, err := exec.Command(tmuxPath, "-L", remoteSocket, "kill-session", "-t", "=first").CombinedOutput(); err != nil {
		t.Fatalf("kill last remote session: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var output []byte
	for time.Now().Before(deadline) {
		if _, sessionErr := runner.Run(ctx, "tmux", "has-session", "-t", proxyID); sessionErr == nil {
			output, _ = runner.Run(ctx, "tmux", "capture-pane", "-p", "-t", proxyID)
			if strings.Contains(string(output), "no sessions on fake-machine") {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := runner.Run(ctx, "tmux", "has-session", "-t", proxyID); err != nil {
		t.Fatalf("local proxy session after last remote removal: %v", err)
	}
	if got := panePID(); got != initialPID {
		t.Fatalf("pane pid at empty-remote prompt = %q, want unchanged %q", got, initialPID)
	}
	if !strings.Contains(string(output), "no sessions on fake-machine") {
		t.Fatalf("empty-remote pane = %q, want retry/quit prompt", output)
	}
}

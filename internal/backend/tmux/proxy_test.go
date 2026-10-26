package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/proxy"
)

type fakeProxyTransport struct {
	failures int
	calls    int
	before   func(int)
}

func (f *fakeProxyTransport) Attach(context.Context, string, string) error {
	f.calls++
	if f.before != nil {
		f.before(f.calls)
	}
	if f.calls <= f.failures {
		return errors.New("connection dropped")
	}
	return nil
}

type fakeProxyClock struct {
	delays []time.Duration
	now    time.Time
}

func (f *fakeProxyClock) Sleep(_ context.Context, delay time.Duration) error {
	f.delays = append(f.delays, delay)
	f.now = f.now.Add(delay)
	return nil
}

func (f *fakeProxyClock) Now() time.Time { return f.now }

func TestRunReconnectResetsBackoffAfterHealthyAttachment(t *testing.T) {
	clock := &fakeProxyClock{}
	transport := &fakeProxyTransport{failures: 3, before: func(call int) {
		if call == 2 {
			// The second attachment stayed healthy for longer than the reset
			// period before dropping.
			clock.now = clock.now.Add(proxy.HealthyAttachmentPeriod)
		}
	}}
	if err := proxy.RunReconnect(context.Background(), transport, clock, nil, "devbox", "$1", time.Second, 30*time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if want := []time.Duration{time.Second, time.Second, 2 * time.Second}; !reflect.DeepEqual(clock.delays, want) {
		t.Fatalf("retry delays = %v, want %v", clock.delays, want)
	}
}

func TestRunReconnectDoublesDelayUntilTransportRecovers(t *testing.T) {
	transport := &fakeProxyTransport{failures: 3}
	clock := &fakeProxyClock{}
	if err := proxy.RunReconnect(context.Background(), transport, clock, nil, "devbox", "$1", time.Second, 3*time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if transport.calls != 4 {
		t.Fatalf("transport calls = %d, want 4", transport.calls)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}; !reflect.DeepEqual(clock.delays, want) {
		t.Fatalf("retry delays = %v, want %v", clock.delays, want)
	}
}

func TestRunReconnectQDuringRetryStopsWithoutAnotherAttach(t *testing.T) {
	transport := &fakeProxyTransport{failures: 1}
	clock := &blockingProxyClock{release: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- proxy.RunReconnect(context.Background(), transport, clock, strings.NewReader("q"), "devbox", "$1", time.Second, 30*time.Second)
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("RunReconnect() error = %v", err)
		}
	case <-time.After(time.Second):
		close(clock.release)
		t.Fatal("RunReconnect() did not handle q during retry")
	}
	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want one before q", transport.calls)
	}
}

type blockingProxyInput struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingProxyInput) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.EOF
}

func (r *blockingProxyInput) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestRunReconnectStopsCancelableReaderBeforeNextAttach(t *testing.T) {
	transport := &fakeProxyTransport{failures: 1}
	clock := &fakeProxyClock{}
	input := &blockingProxyInput{closed: make(chan struct{})}
	if err := proxy.RunReconnect(context.Background(), transport, clock, input, "devbox", "$1", time.Second, time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if transport.calls != 2 {
		t.Fatalf("transport calls = %d, want retry after canceling reader", transport.calls)
	}
}

func TestRunReconnectCancelsRealFileReaderWhenTimerWins(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	transport := &fakeProxyTransport{failures: 1}
	clock := &fakeProxyClock{}
	started := time.Now()
	if err := proxy.RunReconnect(context.Background(), transport, clock, reader, "devbox", "$1", time.Second, time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timer-won cleanup took %s", elapsed)
	}
	if transport.calls != 2 {
		t.Fatalf("transport calls = %d, want retry after canceling file reader", transport.calls)
	}
}

func TestRunReconnectHonorsQAfterOtherKeysDuringOneWait(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := writer.WriteString("xq"); err != nil {
		t.Fatalf("write keys: %v", err)
	}
	_ = writer.Close()

	transport := &fakeProxyTransport{failures: 1}
	clock := &blockingProxyClock{release: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- proxy.RunReconnect(context.Background(), transport, clock, reader, "devbox", "$1", time.Second, 30*time.Second)
	}()
	select {
	case err := <-result:
		close(clock.release)
		if err != nil {
			t.Fatalf("RunReconnect() error = %v", err)
		}
	case <-time.After(time.Second):
		close(clock.release)
		t.Fatal("q after another key during one wait was not honored")
	}
	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want one before q", transport.calls)
	}
}

type exitCodeTransport struct {
	exitCode int
	failures int
	calls    int
}

func (t *exitCodeTransport) Attach(context.Context, string, string) error {
	t.calls++
	if t.calls > t.failures {
		return nil
	}
	return exec.Command("sh", "-c", "exit "+strconv.Itoa(t.exitCode)).Run()
}

func TestRunReconnectDoesNotRetryRemoteCommandExit(t *testing.T) {
	transport := &exitCodeTransport{exitCode: 1, failures: 5}
	clock := &fakeProxyClock{}
	err := proxy.RunReconnect(context.Background(), transport, clock, nil, "devbox", "$1", time.Second, 30*time.Second)
	if err == nil {
		t.Fatal("RunReconnect() error = nil, want the remote command failure")
	}
	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want no retry of exit 1", transport.calls)
	}
	if len(clock.delays) != 0 {
		t.Fatalf("retry delays = %v, want none", clock.delays)
	}
}

func TestRunReconnectRetriesSSHConnectionExit(t *testing.T) {
	transport := &exitCodeTransport{exitCode: 255, failures: 1}
	clock := &fakeProxyClock{}
	if err := proxy.RunReconnect(context.Background(), transport, clock, nil, "devbox", "$1", time.Second, 30*time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if transport.calls != 2 {
		t.Fatalf("transport calls = %d, want retry after exit 255", transport.calls)
	}
	if want := []time.Duration{time.Second}; !reflect.DeepEqual(clock.delays, want) {
		t.Fatalf("retry delays = %v, want %v", clock.delays, want)
	}
}

type blockingProxyClock struct {
	release chan struct{}
}

func (c *blockingProxyClock) Sleep(ctx context.Context, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
		return nil
	}
}

// TestBridgeProxyHistoryRemembersTheRemoteKind pins that the machine proxy
// records which remote multiplexer its pane attaches to, and that a later
// focus event, which can only see tmux session metadata, does not erase it.
// M-o needs that kind to recreate a Herdr bridge instead of a tmux proxy.
func TestBridgeProxyHistoryRemembersTheRemoteKind(t *testing.T) {
	t.Setenv("TMUX_PANE", "%2")
	stateDir := t.TempDir()
	socket := "/tmp/tmux-1000/default"
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-sessions -F #{session_id}\t#{@nexo-proxy}":                                          []byte("$1\trpi\n"),
		"display-message -p #{socket_path}":                                                       []byte(socket + "\n"),
		"display-message -p -t $1 #{session_id}\t#{session_name}\t#{@nexo-proxy}\t#{socket_path}": []byte("$1\trpi\trpi\t" + socket + "\n"),
		"display-message -p -t %2 #{session_id}\t#{session_name}\t#{@nexo-proxy}\t#{socket_path}": []byte("$1\trpi\trpi\t" + socket + "\n"),
	}}
	backend := New(runner, true)
	backend.usageStateDir = stateDir
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"rpi"}}, "")
	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "rpi", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	// Focusing the proxy pane re-records the identity from session metadata.
	if err := backend.RecordFocusEvent(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	fresh := New(runner, true)
	fresh.usageStateDir = stateDir
	entries, err := fresh.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one proxy identity", entries)
	}
	if entry := entries[0]; !entry.Proxy || entry.Machine != "rpi" || entry.RemoteKind != "herdr" {
		t.Fatalf("proxy history = %#v, want rpi proxy with the herdr remote kind", entry)
	}
}

func TestProxyHistorySurvivesReconnectWithChangedNativeID(t *testing.T) {
	t.Setenv("TMUX_PANE", "%2")
	stateDir := t.TempDir()
	socket := "/tmp/tmux-1000/default"
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-sessions -F #{session_id}\t#{@nexo-proxy}":                                          []byte("$1\tdevbox\n"),
		"display-message -p #{socket_path}":                                                       []byte(socket + "\n"),
		"display-message -p -t $1 #{session_id}\t#{session_name}\t#{@nexo-proxy}\t#{socket_path}": []byte("$1\tdevbox\tdevbox\t" + socket + "\n"),
		"display-message -p -t %2 #{session_id}\t#{session_name}\t#{@nexo-proxy}\t#{socket_path}": []byte("$2\tdevbox\tdevbox\t" + socket + "\n"),
	}}
	backend := New(runner, true)
	backend.usageStateDir = stateDir
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"devbox"}}, "")
	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", ""); err != nil {
		t.Fatal(err)
	}
	// The reconnect transport loop never touches history; a later focus event
	// observes the recreated proxy with a new native session id.
	transport := &fakeProxyTransport{failures: 1}
	if err := proxy.RunReconnect(context.Background(), transport, &fakeProxyClock{}, nil, "devbox", "api", time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := backend.RecordFocusEvent(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	fresh := New(runner, true)
	fresh.usageStateDir = stateDir
	entries, err := fresh.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one canonical proxy identity", entries)
	}
	entry := entries[0]
	if entry.Machine != "devbox" || entry.Name != "" || !entry.Proxy || entry.ID != "$2" {
		t.Fatalf("proxy history = %#v, want devbox/api proxy with the recreated native id", entry)
	}

	// A local selection must never see the proxy identity.
	store := containerstate.New(stateDir, "tmux", containerstate.LocalMachine(), socket)
	if err := store.Record(containerstate.Entry{ID: "$7", Name: "local", Machine: containerstate.LocalMachine()}); err != nil {
		t.Fatal(err)
	}
	freshEntries, err := fresh.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range freshEntries {
		if current.Proxy && current.ID == "$7" {
			t.Fatalf("proxy exclusion broken: %#v", freshEntries)
		}
	}
}

func TestProxySessionIDMatchesMetadataExactly(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-sessions -F #{session_id}\t#{@nexo-proxy}": []byte("$0\tdevbox/prod-long\n$1\tdevbox/prod\n"),
	}}
	id, err := New(runner).ProxySessionID(context.Background(), "devbox", "prod")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if id != "$1" {
		t.Fatalf("ProxySessionID() = %q, want $1", id)
	}
}

func TestProxySwitchesExistingSessionWithoutCreatingDuplicate(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-sessions -F #{session_id}\t#{@nexo-proxy}": []byte("$1\tdevbox\n"),
	}}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", "$1"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 7 {
		t.Fatalf("tmux calls = %#v, want list, remote switch, three options, switch, and classification", runner.calls)
	}
	if runner.calls[0].args[1] != "list-sessions" || runner.calls[1].name != "ssh" || runner.calls[2].args[1] != "set-option" || runner.calls[3].args[1] != "set-option" || runner.calls[4].args[1] != "set-option" || runner.calls[5].args[0] != "switch-client" || runner.calls[6].args[1] != "display-message" {
		t.Fatalf("tmux calls = %#v, want existing proxy switch", runner.calls)
	}
	if got := runner.calls[5].args[2]; got != "$1" {
		t.Fatalf("switch target = %q, want stable session id", got)
	}
	if got := runner.calls[4].args[5]; got != "off" || runner.calls[4].args[4] != "detach-on-destroy" {
		t.Fatalf("detach-on-destroy option = %#v, want off on proxy session", runner.calls[4].args)
	}
}

func TestMOMachineProxyNeverRetargetsExistingRemoteClient(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-sessions -F #{session_id}\t#{@nexo-proxy}":                                          []byte("$1\tdevbox\n"),
		"display-message -p -t $1 #{session_id}\t#{session_name}\t#{@nexo-proxy}\t#{socket_path}": []byte("$1\tdevbox\tdevbox\t/tmp/tmux-test\n"),
	}}
	backend := New(runner, true)
	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", ""); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	for _, call := range runner.calls {
		if call.name == "ssh" || (len(call.args) > 1 && call.args[1] == "respawn-pane") {
			t.Fatalf("M-o retargeted or restarted existing proxy: %#v", runner.calls)
		}
	}
}

func TestProxyCreatesSessionWithQuotedAttachCommand(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"list-sessions -F #{session_id}\t#{@nexo-proxy}": nil,
		},
		outputFunc: func(_ string, args []string) ([]byte, bool) {
			if len(args) > 1 && args[1] == "new-session" {
				return []byte("$2\n"), true
			}
			return nil, false
		},
	}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "/tmp/my config.toml")
	if err := backend.Proxy(context.Background(), proxy.RemoteKindTmux, "devbox", "$1"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if got := runner.calls[2].args[1]; got != "new-session" {
		t.Fatalf("third tmux call = %#v, want new-session", runner.calls[2])
	}
	if got := runner.calls[2].args[5]; got != "#{session_id}" {
		t.Fatalf("new-session format = %q, want session id format", got)
	}
	command := runner.calls[2].args[len(runner.calls[2].args)-1]
	if !strings.Contains(command, " --backend tmux ") || !strings.Contains(command, " remote attach --initial '") || !strings.Contains(command, "--config-file '/tmp/my config.toml'") {
		t.Fatalf("proxy pane command = %q, want backend/config flags before positional boundary", command)
	}
	if strings.HasPrefix(command, "NEXO_PROXY_ID=") {
		t.Fatalf("proxy pane command = %q, want no unused NEXO_PROXY_ID prefix", command)
	}
}

func TestTransportArgsHonorSSHConfigOnlyForSSH(t *testing.T) {
	keepalive := []string{"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	remote := config.RemoteConfig{SSHConfig: "/tmp/ssh config", Transport: "ssh"}
	sshWant := append([]string{"ssh", "-t"}, keepalive...)
	sshWant = append(sshWant, "-F", "/tmp/ssh config", "devbox")
	got := proxy.TransportArgs(remote, "tmux", proxy.RemoteKindTmux, "devbox", "")
	if !reflect.DeepEqual(got[:len(sshWant)], sshWant) || !strings.Contains(got[len(got)-1], "session_last_attached") || !strings.Contains(got[len(got)-1], "attach-session") {
		t.Fatalf("ssh transport args = %#v, want keepalive and last-attached resolver", got)
	}
	remote.Transport = "mosh"
	if got := proxy.TransportArgs(remote, "tmux", proxy.RemoteKindTmux, "devbox", ""); len(got) != 6 || got[0] != "mosh" || got[2] != "--" || got[3] != "sh" || got[4] != "-c" || !strings.Contains(got[5], "session_last_attached") {
		t.Fatalf("mosh transport args = %#v, want shell resolver", got)
	}
	remote.Transport = "et"
	if got := proxy.TransportArgs(remote, "tmux", proxy.RemoteKindTmux, "devbox", ""); len(got) != 4 || got[0] != "et" || !strings.HasPrefix(got[3], "sh -c '") || !strings.Contains(got[3], "session_last_attached") {
		t.Fatalf("et transport args = %#v, want quoted shell resolver", got)
	}
}

func TestSendPaneKeysUsesOptionTerminatorAndKeepsKeyBoundaries(t *testing.T) {
	runner := &fakeRunner{}
	if err := New(runner).SendPaneKeys(context.Background(), "%9", []string{"C-v", "M-v"}); err != nil {
		t.Fatalf("SendPaneKeys() error = %v", err)
	}
	want := []string{"-u", "send-keys", "-t", "%9", "--", "C-v", "M-v"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestSendPaneKeysEscapesTmuxCommandSeparators(t *testing.T) {
	cases := []struct {
		keys []string
		want []string
	}{
		{keys: []string{";", "-l"}, want: []string{`\;`, "-l"}},
		{keys: []string{"x;"}, want: []string{`x\;`}},
		{keys: []string{"x;;"}, want: []string{`x;\;`}},
		{keys: []string{"a;b", `x\;`}, want: []string{"a;b", `x\;`}},
	}
	for _, test := range cases {
		runner := &fakeRunner{}
		if err := New(runner).SendPaneKeys(context.Background(), "%9", test.keys); err != nil {
			t.Fatalf("SendPaneKeys(%#v) error = %v", test.keys, err)
		}
		want := append([]string{"-u", "send-keys", "-t", "%9", "--"}, test.want...)
		if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
			t.Fatalf("keys %#v: calls = %#v, want %#v", test.keys, runner.calls, want)
		}
	}
}

func TestSendPaneKeysWithoutKeysIsNoop(t *testing.T) {
	runner := &fakeRunner{}
	if err := New(runner).SendPaneKeys(context.Background(), "%9", nil); err != nil {
		t.Fatalf("SendPaneKeys() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want no tmux invocation", runner.calls)
	}
}

func TestSendPaneTextUsesLiteralAndOptionBoundaries(t *testing.T) {
	runner := &fakeRunner{}
	if err := New(runner).SendPaneText(context.Background(), "%9", "-x; text;"); err != nil {
		t.Fatalf("SendPaneText() error = %v", err)
	}
	want := []string{"-u", "send-keys", "-t", "%9", "-l", "--", `-x; text\;`}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestProxyBridgesToHerdrInTheSameMachineProxySession(t *testing.T) {
	runner := &fakeRunner{
		outputFunc: func(_ string, args []string) ([]byte, bool) {
			if len(args) > 1 && args[1] == "new-session" {
				return []byte("$2\n"), true
			}
			return nil, false
		},
	}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "rpi", "w2"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	var created string
	for _, call := range runner.calls {
		if len(call.args) > 1 && call.args[1] == "new-session" {
			created = call.args[len(call.args)-1]
		}
		if call.name == "ssh" {
			t.Fatalf("Proxy() used ssh to create the local proxy: %#v", runner.calls)
		}
	}
	if !strings.Contains(created, "--backend tmux remote attach --remote-kind herdr --initial 'w2' -- 'rpi'") {
		t.Fatalf("bridge pane command = %q, want the herdr attach command", created)
	}
}

func TestProxySwitchesAnExistingHerdrBridgeClientWithoutRespawning(t *testing.T) {
	args := proxy.SwitchSSHArgs(config.RemoteConfig{}, "tmux", proxy.RemoteKindHerdr, "rpi", "w3")
	runner := &fakeRunner{
		sequences: map[string][][]byte{
			"list-sessions -F #{session_id}\t#{@nexo-proxy}": {[]byte("$1\trpi\n")},
		},
	}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	if err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "rpi", "w3"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	var switched []string
	for _, call := range runner.calls {
		if call.name == "ssh" {
			switched = append(switched, strings.Join(call.args, " "))
		}
		if len(call.args) > 1 && call.args[1] == "respawn-pane" {
			t.Fatalf("Proxy() respawned the bridge pane: %#v", runner.calls)
		}
	}
	if len(switched) != 1 || switched[0] != strings.Join(args, " ") || !strings.Contains(switched[0], "workspace focus") {
		t.Fatalf("switch calls = %#v, want one herdr workspace focus", switched)
	}
	if len(runner.calls) > 1 && runner.calls[0].args[1] != "list-sessions" {
		t.Fatalf("first tmux call = %#v, want the proxy lookup", runner.calls[0])
	}
}

func TestProxyReportsAMissingHerdrWorkspaceInsteadOfStartingOne(t *testing.T) {
	exitTwo := exec.Command("sh", "-c", "exit 2").Run()
	if exitTwo == nil {
		t.Fatal("expected a non-zero exit from the helper shell")
	}
	args := proxy.SwitchSSHArgs(config.RemoteConfig{}, "tmux", proxy.RemoteKindHerdr, "rpi", "w9")
	runner := &fakeRunner{
		sequences: map[string][][]byte{
			"list-sessions -F #{session_id}\t#{@nexo-proxy}": {[]byte("$1\trpi\n")},
		},
		fail: map[string]error{commandKey(args): exitTwo},
	}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	err := backend.Proxy(context.Background(), proxy.RemoteKindHerdr, "rpi", "w9")
	if err == nil || err.Error() != "no workspace w9 on rpi" {
		t.Fatalf("Proxy() error = %v, want the missing-workspace diagnostic", err)
	}
	for _, call := range runner.calls {
		if len(call.args) > 1 && (call.args[1] == "respawn-pane" || call.args[1] == "new-session") {
			t.Fatalf("missing workspace restarted the pane: %#v", runner.calls)
		}
		if strings.Contains(strings.Join(call.args, " "), "workspace create") {
			t.Fatalf("missing workspace created remote state: %#v", runner.calls)
		}
	}
}

func TestProxyRejectsAnUnsupportedRemoteKind(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	if err := backend.Proxy(context.Background(), "zellij", "rpi", "w2"); err == nil {
		t.Fatal("Proxy() accepted an unsupported remote kind")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want none before validation", runner.calls)
	}
}

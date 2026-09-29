package tmux

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
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
			clock.now = clock.now.Add(healthyAttachmentPeriod)
		}
	}}
	if err := RunReconnect(context.Background(), transport, clock, nil, "devbox", "$1", time.Second, 30*time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if want := []time.Duration{time.Second, time.Second, 2 * time.Second}; !reflect.DeepEqual(clock.delays, want) {
		t.Fatalf("retry delays = %v, want %v", clock.delays, want)
	}
}

func TestRunReconnectDoublesDelayUntilTransportRecovers(t *testing.T) {
	transport := &fakeProxyTransport{failures: 3}
	clock := &fakeProxyClock{}
	if err := RunReconnect(context.Background(), transport, clock, nil, "devbox", "$1", time.Second, 3*time.Second); err != nil {
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
		result <- RunReconnect(context.Background(), transport, clock, strings.NewReader("q"), "devbox", "$1", time.Second, 30*time.Second)
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
	if err := RunReconnect(context.Background(), transport, clock, input, "devbox", "$1", time.Second, time.Second); err != nil {
		t.Fatalf("RunReconnect() error = %v", err)
	}
	if transport.calls != 2 {
		t.Fatalf("transport calls = %d, want retry after canceling reader", transport.calls)
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
		"list-sessions -F #{session_id}\t#{@nexo-proxy}": []byte("$1\tdevbox/$1\n"),
	}}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	if err := backend.Proxy(context.Background(), "devbox", "$1"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("tmux calls = %#v, want list, two options, and switch", runner.calls)
	}
	if runner.calls[0].args[0] != "list-sessions" || runner.calls[1].args[0] != "set-option" || runner.calls[2].args[0] != "set-option" || runner.calls[3].args[0] != "switch-client" {
		t.Fatalf("tmux calls = %#v, want existing proxy switch", runner.calls)
	}
	if got := runner.calls[3].args[2]; got != "$1" {
		t.Fatalf("switch target = %q, want stable session id", got)
	}
}

func TestProxyCreatesSessionWithQuotedAttachCommand(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"list-sessions -F #{session_id}\t#{@nexo-proxy}": nil,
		},
		outputFunc: func(_ string, args []string) ([]byte, bool) {
			if len(args) > 0 && args[0] == "new-session" {
				return []byte("$2\n"), true
			}
			return nil, false
		},
	}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "/tmp/my config.toml")
	if err := backend.Proxy(context.Background(), "devbox", "$1"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if got := runner.calls[1].args[0]; got != "new-session" {
		t.Fatalf("second tmux call = %#v, want new-session", runner.calls[1])
	}
	if got := runner.calls[1].args[4]; got != "#{session_id}" {
		t.Fatalf("new-session format = %q, want session id format", got)
	}
	command := runner.calls[1].args[len(runner.calls[1].args)-1]
	if !strings.Contains(command, " --backend tmux ") || !strings.Contains(command, " remote attach -- '") || !strings.Contains(command, "--config-file '/tmp/my config.toml'") {
		t.Fatalf("proxy pane command = %q, want backend/config flags before positional boundary", command)
	}
}

func TestTransportArgsHonorSSHConfigOnlyForSSH(t *testing.T) {
	remote := config.RemoteConfig{SSHConfig: "/tmp/ssh config", Transport: "ssh"}
	if got, want := transportArgs(remote, "devbox", "$1"), []string{"ssh", "-t", "-F", "/tmp/ssh config", "devbox", "tmux attach-session -t '$1'"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ssh transport args = %#v, want %#v", got, want)
	}
	remote.Transport = "mosh"
	if got, want := transportArgs(remote, "devbox", "$1"), []string{"mosh", "devbox", "--", "tmux", "attach-session", "-t", "$1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mosh transport args = %#v, want %#v", got, want)
	}
	remote.Transport = "et"
	if got, want := transportArgs(remote, "devbox", "$1"), []string{"et", "devbox", "-c", "tmux attach-session -t '$1'"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("et transport args = %#v, want %#v", got, want)
	}
}

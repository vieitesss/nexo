package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
)

type fakeProxyTransport struct {
	failures int
	calls    int
}

func (f *fakeProxyTransport) Attach(context.Context, string, string) error {
	f.calls++
	if f.calls <= f.failures {
		return errors.New("connection dropped")
	}
	return nil
}

type fakeProxyClock struct {
	delays []time.Duration
}

func (f *fakeProxyClock) Sleep(_ context.Context, delay time.Duration) error {
	f.delays = append(f.delays, delay)
	return nil
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

func TestProxySwitchesExistingSessionWithoutCreatingDuplicate(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	if err := backend.Proxy(context.Background(), "devbox", "$1"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("tmux calls = %#v, want has-session, two options, and switch", runner.calls)
	}
	if runner.calls[0].args[0] != "has-session" || runner.calls[1].args[0] != "set-option" || runner.calls[2].args[0] != "set-option" || runner.calls[3].args[0] != "switch-client" {
		t.Fatalf("tmux calls = %#v, want existing proxy switch", runner.calls)
	}
}

func TestProxyCreatesSessionWithQuotedAttachCommand(t *testing.T) {
	runner := &fakeRunner{fail: map[string]error{
		"has-session -t =devbox/$1": errors.New("can't find session: devbox/$1"),
	}}
	backend := New(runner, true)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "/tmp/my config.toml")
	if err := backend.Proxy(context.Background(), "devbox", "$1"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if got := runner.calls[1].args[0]; got != "new-session" {
		t.Fatalf("second tmux call = %#v, want new-session", runner.calls[1])
	}
	command := runner.calls[1].args[len(runner.calls[1].args)-1]
	if !strings.Contains(command, " remote attach '") || !strings.Contains(command, "--config-file '/tmp/my config.toml'") {
		t.Fatalf("proxy pane command = %q, want quoted attach command and config path", command)
	}
}

func TestTransportArgsHonorSSHConfigOnlyForSSH(t *testing.T) {
	remote := config.RemoteConfig{SSHConfig: "/tmp/ssh config", Transport: "ssh"}
	if got, want := transportArgs(remote, "devbox", "$1"), []string{"ssh", "-F", "/tmp/ssh config", "devbox", "tmux attach-session -t '$1'"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ssh transport args = %#v, want %#v", got, want)
	}
	remote.Transport = "mosh"
	if got, want := transportArgs(remote, "devbox", "$1"), []string{"mosh", "devbox", "--", "tmux", "attach-session", "-t", "$1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mosh transport args = %#v, want %#v", got, want)
	}
}

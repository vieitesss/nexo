package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

// ProxyTransport attaches to a remote container. An attachment returning an
// error is considered a dropped connection and is retried by Reconnect.
type ProxyTransport interface {
	Attach(context.Context, string, string) error
}

// ProxyClock is the sleep seam for reconnect backoff tests.
type ProxyClock interface {
	Sleep(context.Context, time.Duration) error
}

type systemProxyClock struct{}

func (systemProxyClock) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RunReconnect attaches repeatedly after transport failures. A q read while
// waiting for the next attempt closes the proxy without touching the remote
// container.
func RunReconnect(ctx context.Context, transport ProxyTransport, clock ProxyClock, input io.Reader, machine, container string, initial, maximum time.Duration) error {
	if transport == nil {
		return errors.New("proxy transport is required")
	}
	if clock == nil {
		clock = systemProxyClock{}
	}
	if initial <= 0 {
		initial = config.DefaultReconnectInitial
	}
	if maximum <= 0 {
		maximum = config.DefaultReconnectMax
	}
	if initial > maximum {
		initial = maximum
	}

	delay := initial
	for {
		err := transport.Attach(ctx, machine, container)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		quit, sleepErr := waitProxyRetry(ctx, clock, input, delay)
		if sleepErr != nil {
			return sleepErr
		}
		if quit {
			return nil
		}
		if delay < maximum {
			delay *= 2
			if delay > maximum || delay < 0 {
				delay = maximum
			}
		}
	}
}

// Reconnect is a short alias for callers that prefer the capability's
// operation name.
func Reconnect(ctx context.Context, transport ProxyTransport, clock ProxyClock, input io.Reader, machine, container string, initial, maximum time.Duration) error {
	return RunReconnect(ctx, transport, clock, input, machine, container, initial, maximum)
}

type proxyKey struct {
	key byte
	err error
}

func waitProxyRetry(ctx context.Context, clock ProxyClock, input io.Reader, delay time.Duration) (bool, error) {
	if input != nil {
		if key, available := availableProxyKey(input); available {
			return key == 'q' || key == 'Q', nil
		}
	}
	sleepDone := make(chan error, 1)
	go func() { sleepDone <- clock.Sleep(ctx, delay) }()
	if input == nil {
		return false, <-sleepDone
	}

	keyRead := make(chan proxyKey, 1)
	go func() {
		var one [1]byte
		_, err := input.Read(one[:])
		keyRead <- proxyKey{key: one[0], err: err}
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case err := <-sleepDone:
		return false, err
	case result := <-keyRead:
		if result.err != nil {
			// EOF or an unavailable input should not prevent a retry, but the
			// configured delay still applies.
			return false, <-sleepDone
		}
		if result.key == 'q' || result.key == 'Q' {
			return true, nil
		}
		return false, <-sleepDone
	}
}

// availableProxyKey handles buffered test and prompt readers without racing a
// zero-duration fake clock. Terminal files use the goroutine path above.
func availableProxyKey(input io.Reader) (byte, bool) {
	if source, ok := input.(interface{ Len() int }); ok && source.Len() > 0 {
		var one [1]byte
		if count, err := input.Read(one[:]); count == 1 && err == nil {
			return one[0], true
		}
	}
	if source, ok := input.(interface{ Buffered() int }); ok && source.Buffered() > 0 {
		var one [1]byte
		if count, err := input.Read(one[:]); count == 1 && err == nil {
			return one[0], true
		}
	}
	return 0, false
}

type commandProxyTransport struct {
	config config.RemoteConfig
}

func (t commandProxyTransport) Attach(ctx context.Context, machine, container string) error {
	args := transportArgs(t.config, machine, container)
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func transportArgs(remote config.RemoteConfig, machine, container string) []string {
	transport := remote.EffectiveTransport()
	prefix := make([]string, 0, 8)
	switch transport {
	case "mosh":
		prefix = append(prefix, "mosh", machine, "--")
	case "et":
		prefix = append(prefix, "et", machine, "--")
	default:
		prefix = append(prefix, "ssh")
		if strings.TrimSpace(remote.SSHConfig) != "" {
			prefix = append(prefix, "-F", remote.SSHConfig)
		}
		prefix = append(prefix, machine, "tmux attach-session -t "+shellQuote(container))
		return prefix
	}
	return append(prefix, "tmux", "attach-session", "-t", container)
}

// ConfigureRemoteConfig supplies proxy settings and the optional explicit
// config path forwarded to the child process in a proxy pane.
func (b *Backend) ConfigureRemoteConfig(remote config.RemoteConfig, configPath string) {
	if b == nil {
		return
	}
	b.proxyConfig = remote
	b.proxyConfigPath = configPath
}

// Proxy opens or switches to a local proxy container. The remote attachment
// is the only process in its pane; closing this local session cannot close the
// remote container.
func (b *Backend) Proxy(ctx context.Context, machine, container string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux remote proxy requires a command runner")
	}
	machine = strings.TrimSpace(machine)
	container = strings.TrimSpace(container)
	if machine == "" {
		return errors.New("tmux remote proxy requires a machine")
	}
	if strings.HasPrefix(machine, "-") {
		return fmt.Errorf("machine %q starts with '-'", machine)
	}
	if container == "" {
		return errors.New("tmux remote proxy requires a container")
	}

	name := machine + "/" + container
	_, err := b.run(ctx, "has-session", "-t", "="+name)
	exists := err == nil
	if err != nil && !isMissingSession(err) {
		return err
	}
	if !exists {
		if _, err := b.run(ctx, "new-session", "-d", "-s", name, b.proxyPaneCommand(machine, container)); err != nil {
			return err
		}
	}
	// Set these on the proxy session only. Never use -g: the user's local
	// prefix and status bar must remain untouched.
	if _, err := b.run(ctx, "set-option", "-t", "="+name, "prefix", "None"); err != nil {
		return err
	}
	if _, err := b.run(ctx, "set-option", "-t", "="+name, "status", "off"); err != nil {
		return err
	}
	if b.Inside {
		_, err = b.run(ctx, "switch-client", "-t", "="+name)
		return err
	}
	return b.runInteractive(ctx, "attach-session", "-t", "="+name)
}

func (b *Backend) proxyPaneCommand(machine, container string) string {
	binary := proxyBinary()
	parts := []string{shellQuote(binary), "remote", "attach", shellQuote(machine), shellQuote(container)}
	if b.proxyConfigPath != "" {
		parts = append(parts, "--config-file", shellQuote(b.proxyConfigPath))
	}
	return strings.Join(parts, " ")
}

func proxyBinary() string {
	binary := os.Args[0]
	if resolved, err := exec.LookPath(binary); err == nil {
		binary = resolved
	}
	if absolute, err := filepath.Abs(binary); err == nil {
		binary = absolute
	}
	return binary
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// NewCommandTransport returns the process transport used by the hidden attach
// command. It is exported for command wiring while tests use ProxyTransport.
func NewCommandTransport(remote config.RemoteConfig) ProxyTransport {
	return commandProxyTransport{config: remote}
}

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ ProxyTransport = commandProxyTransport{}

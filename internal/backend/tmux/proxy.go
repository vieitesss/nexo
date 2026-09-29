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

	"github.com/charmbracelet/x/term"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"golang.org/x/sys/unix"
)

const (
	proxySessionOption      = "@nexo-proxy"
	healthyAttachmentPeriod = time.Minute
)

// ProxyTransport attaches to a remote container. An attachment returning an
// error is considered a dropped connection and is retried by RunReconnect.
type ProxyTransport interface {
	Attach(context.Context, string, string) error
}

// ProxyClock is the sleep seam for reconnect backoff tests. Implementations
// may also provide Now() for deterministic healthy-attachment tests.
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

// RunReconnect attaches repeatedly after SSH transport failures. A q read
// while waiting for the next attempt closes the proxy without touching the
// remote container.
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
		started := proxyNow(clock)
		err := transport.Attach(ctx, machine, container)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if proxyNow(clock).Sub(started) >= healthyAttachmentPeriod {
			// A connection that remained up for a meaningful period marks the
			// previous outage recovered. The next outage gets the initial delay.
			delay = initial
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

type proxyClockNow interface {
	Now() time.Time
}

func proxyNow(clock ProxyClock) time.Time {
	if clockWithNow, ok := clock.(proxyClockNow); ok {
		return clockWithNow.Now()
	}
	return time.Now()
}

type proxyKey struct {
	key byte
	err error
}

// waitProxyRetry owns input for the complete retry wait. It always stops and
// joins the reader before returning, so an attachment cannot race a stale
// reader consuming its input. Terminal state is restored before the caller
// starts the next attachment.
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

	reader, stop, done, restore, ok := startProxyInput(input)
	if !ok {
		// A reader that cannot be canceled cannot safely run concurrently with
		// the next attachment. Buffered readers were handled above; for an
		// arbitrary blocking reader, honor the retry timer without starting a
		// stale read goroutine.
		return false, <-sleepDone
	}
	keyRead := make(chan proxyKey, 1)
	go func() {
		defer close(done)
		var one [1]byte
		_, err := reader.Read(one[:])
		keyRead <- proxyKey{key: one[0], err: err}
	}()

	cleanup := func() {
		stop()
		<-done
		restore()
	}
	select {
	case <-ctx.Done():
		cleanup()
		return false, ctx.Err()
	case err := <-sleepDone:
		cleanup()
		return false, err
	case result := <-keyRead:
		cleanup()
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

// startProxyInput returns a cancelable one-byte reader and a cleanup function
// for terminal state. os.Stdin is duplicated before reading so canceling a
// retry never closes the process's standard input.
func startProxyInput(input io.Reader) (io.Reader, func(), chan struct{}, func(), bool) {
	restore := func() {}

	if file, isFile := input.(*os.File); isFile {
		var state *term.State
		if term.IsTerminal(file.Fd()) {
			var err error
			state, err = term.MakeRaw(file.Fd())
			if err != nil {
				return nil, nil, nil, nil, false
			}
			restore = func() { _ = term.Restore(file.Fd(), state) }
		}
		fd, err := unix.Dup(int(file.Fd()))
		if err != nil {
			restore()
			return nil, nil, nil, nil, false
		}
		duplicate := os.NewFile(uintptr(fd), file.Name())
		finished := make(chan struct{})
		return duplicate, func() { _ = duplicate.Close() }, finished, restore, true
	}

	closer, canClose := input.(io.Closer)
	if !canClose {
		return nil, nil, nil, nil, false
	}
	finished := make(chan struct{})
	return input, func() { _ = closer.Close() }, finished, restore, true
}

// availableProxyKey handles buffered test and prompt readers without racing a
// zero-duration fake clock. Terminal files use the cancelable reader path.
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
	switch transport {
	case "mosh":
		return []string{"mosh", machine, "--", "tmux", "attach-session", "-t", container}
	case "et":
		command := "tmux attach-session -t " + shellQuote(container)
		return []string{"et", machine, "-c", command}
	default:
		args := []string{"ssh", "-t"}
		if strings.TrimSpace(remote.SSHConfig) != "" {
			args = append(args, "-F", remote.SSHConfig)
		}
		args = append(args, machine, "tmux attach-session -t "+shellQuote(container))
		return args
	}
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

// ValidateProxyIdentity rejects values that cannot safely be represented by a
// tmux session name or by the session metadata used to find a proxy.
func ValidateProxyIdentity(machine, container string) error {
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
	if strings.HasSuffix(container, ";") {
		return errors.New("tmux remote proxy container cannot end with ';'")
	}
	if strings.ContainsAny(machine+container, "\x00\r\n\t") {
		return errors.New("tmux remote proxy names cannot contain NUL, newline, or tab")
	}
	return nil
}

// EscapeSessionName escapes tmux's format marker while retaining the display
// name's bytes (apart from tmux's own canonicalization of '.' and ':').
func EscapeSessionName(name string) string {
	return strings.ReplaceAll(name, "#", "##")
}

// ProxySessionID finds the exact proxy identity and returns its stable tmux
// session ID. Session names are display values only and are never targets.
func (b *Backend) ProxySessionID(ctx context.Context, machine, container string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("tmux remote proxy requires a command runner")
	}
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return "", err
	}
	identity := strings.TrimSpace(machine) + "/" + strings.TrimSpace(container)
	output, err := b.run(ctx, "list-sessions", "-F", "#{session_id}\t#{@nexo-proxy}")
	if err != nil {
		if isNoServer(err) {
			return "", nil
		}
		return "", err
	}
	for _, line := range strings.Split(strings.TrimRight(string(output), "\r\n"), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 2 && fields[1] == identity {
			return fields[0], nil
		}
	}
	return "", nil
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
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return err
	}

	name := machine + "/" + container
	sessionID, err := b.ProxySessionID(ctx, machine, container)
	if err != nil {
		return err
	}
	if sessionID == "" {
		output, err := b.run(ctx, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", EscapeSessionName(name), b.proxyPaneCommand(machine, container))
		if err != nil {
			return err
		}
		sessionID = strings.TrimSpace(string(output))
		if newline := strings.IndexByte(sessionID, '\n'); newline >= 0 {
			sessionID = sessionID[:newline]
		}
		if sessionID == "" {
			return errors.New("tmux remote proxy did not report a session id")
		}
		if _, err := b.run(ctx, "set-option", "-t", sessionID, proxySessionOption, name); err != nil {
			return err
		}
	}
	// Set these on the proxy session only. Never use -g: the user's local
	// prefix and status bar must remain untouched.
	if _, err := b.run(ctx, "set-option", "-t", sessionID, "prefix", "None"); err != nil {
		return err
	}
	if _, err := b.run(ctx, "set-option", "-t", sessionID, "status", "off"); err != nil {
		return err
	}
	if b.Inside {
		_, err = b.run(ctx, "switch-client", "-t", sessionID)
		return err
	}
	return b.runInteractive(ctx, "attach-session", "-t", sessionID)
}

func (b *Backend) proxyPaneCommand(machine, container string) string {
	binary := proxyBinary()
	parts := []string{shellQuote(binary), "--backend", "tmux"}
	if b.proxyConfigPath != "" {
		parts = append(parts, "--config-file", shellQuote(b.proxyConfigPath))
	}
	parts = append(parts, "remote", "attach", "--", shellQuote(machine), shellQuote(container))
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

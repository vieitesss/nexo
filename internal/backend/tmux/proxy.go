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
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"golang.org/x/sys/unix"
)

const (
	proxySessionOption      = "@nexo-proxy"
	healthyAttachmentPeriod = time.Minute
	// connectionExitCode is the status ssh reserves for its own connection
	// failures. Any other exit status comes from the remote command.
	connectionExitCode = 255
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
		if !isConnectionError(err) {
			return err
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

// isConnectionError reports whether err is a dropped connection worth
// retrying. ssh reserves exit status 255 for its own connection errors; every
// other exit status comes from the remote command and ends the attach loop.
// Errors without an exit status come from non-process transports and are
// treated as dropped connections.
func isConnectionError(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == connectionExitCode
	}
	return true
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

// waitProxyRetry owns input for the complete retry wait. It keeps the terminal
// raw and keeps reading until q/Q, cancellation, or a read error, discarding
// every other byte so no key typed during the wait leaks to the next
// attachment. It always stops and joins the reader before returning, so an
// attachment cannot race a stale reader consuming its input. Terminal state is
// restored before the caller starts the next attachment.
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
	fmt.Fprintf(os.Stderr, "reconnecting in %s — press q to close\n", delay)
	keyRead := make(chan error, 1)
	go func() {
		defer close(done)
		var one [1]byte
		for {
			count, err := reader.Read(one[:])
			if err != nil {
				keyRead <- err
				return
			}
			if count == 0 {
				continue
			}
			if one[0] == 'q' || one[0] == 'Q' {
				keyRead <- nil
				return
			}
			// Discard any other byte and keep owning the terminal until q/Q,
			// cancellation, or a read error.
		}
	}()

	cleanup := func() {
		stop()
		<-done
		if closer, ok := reader.(io.Closer); ok {
			_ = closer.Close()
		}
		restore()
	}
	select {
	case <-ctx.Done():
		cleanup()
		return false, ctx.Err()
	case err := <-sleepDone:
		cleanup()
		return false, err
	case resultErr := <-keyRead:
		cleanup()
		if resultErr != nil {
			// EOF or an unavailable input should not prevent a retry, but the
			// configured delay still applies.
			return false, <-sleepDone
		}
		return true, nil
	}
}

// startProxyInput returns a cancelable one-byte reader and a cleanup function
// for terminal state. os.File inputs use unix.Poll with a cancellation pipe so
// stopping a retry wakes a genuinely blocked read. os.Stdin is duplicated
// before reading so canceling a retry never closes the process's standard input.
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
		cancel := make([]int, 2)
		if err := unix.Pipe(cancel); err != nil {
			_ = unix.Close(fd)
			restore()
			return nil, nil, nil, nil, false
		}
		reader := &pollProxyReader{fd: fd, cancelFD: cancel[0], cancelWriteFD: cancel[1]}
		finished := make(chan struct{})
		return reader, reader.cancel, finished, restore, true
	}

	closer, canClose := input.(io.Closer)
	if !canClose {
		return nil, nil, nil, nil, false
	}
	finished := make(chan struct{})
	return input, func() { _ = closer.Close() }, finished, restore, true
}

// pollProxyReader waits for either input or a cancellation byte. Closing a
// duplicated file descriptor does not interrupt an in-flight Linux read, but
// polling a self-pipe does, which keeps retry cleanup bounded on terminals and
// pipes alike.
type pollProxyReader struct {
	fd            int
	cancelFD      int
	cancelWriteFD int
	cancelOnce    sync.Once
	closeOnce     sync.Once
}

func (r *pollProxyReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	pollfds := []unix.PollFd{
		{Fd: int32(r.fd), Events: unix.POLLIN},
		{Fd: int32(r.cancelFD), Events: unix.POLLIN},
	}
	for {
		_, err := unix.Poll(pollfds, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if pollfds[1].Revents != 0 {
			return 0, errProxyInputCanceled
		}
		if pollfds[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(r.fd, buffer)
		if err == unix.EINTR {
			continue
		}
		return n, err
	}
}

func (r *pollProxyReader) cancel() {
	r.cancelOnce.Do(func() {
		if r.cancelWriteFD < 0 {
			return
		}
		_, _ = unix.Write(r.cancelWriteFD, []byte{1})
		_ = unix.Close(r.cancelWriteFD)
		r.cancelWriteFD = -1
	})
}

func (r *pollProxyReader) Close() error {
	r.closeOnce.Do(func() {
		_ = unix.Close(r.fd)
		_ = unix.Close(r.cancelFD)
		if r.cancelWriteFD >= 0 {
			_ = unix.Close(r.cancelWriteFD)
			r.cancelWriteFD = -1
		}
	})
	return nil
}

var errProxyInputCanceled = errors.New("proxy input canceled")

// availableProxyKey handles buffered test and prompt readers without racing a
// zero-duration fake clock. It drains buffered bytes until it finds q/Q,
// discarding the rest; if the buffer empties first it reports no key so the
// caller still honors the retry delay. Terminal files use the cancelable
// reader path instead.
func availableProxyKey(input io.Reader) (byte, bool) {
	for proxyInputBuffered(input) {
		var one [1]byte
		count, err := input.Read(one[:])
		if count != 1 || err != nil {
			return 0, false
		}
		if one[0] == 'q' || one[0] == 'Q' {
			return one[0], true
		}
	}
	return 0, false
}

func proxyInputBuffered(input io.Reader) bool {
	if source, ok := input.(interface{ Len() int }); ok && source.Len() > 0 {
		return true
	}
	if source, ok := input.(interface{ Buffered() int }); ok && source.Buffered() > 0 {
		return true
	}
	return false
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
	container = CanonicalContainerName(container)
	target := "=" + container
	switch transport {
	case "mosh":
		return []string{"mosh", machine, "--", "tmux", "attach-session", "-t", target}
	case "et":
		command := "tmux attach-session -t " + shellQuote(target)
		return []string{"et", machine, "-c", command}
	default:
		// ServerAliveInterval/CountMax let ssh notice a dropped link instead of
		// blocking the pane indefinitely. They apply to the attachment only;
		// listing and other remote commands use plain SSH.
		args := []string{"ssh", "-t", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
		if strings.TrimSpace(remote.SSHConfig) != "" {
			args = append(args, "-F", remote.SSHConfig)
		}
		args = append(args, machine, "tmux attach-session -t "+shellQuote(target))
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

// CanonicalContainerName applies tmux's session-name canonicalization for
// punctuation that tmux rewrites when creating a session.
func CanonicalContainerName(name string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(name)
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
	container = CanonicalContainerName(container)
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
	container = CanonicalContainerName(strings.TrimSpace(container))
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

// NewCommandTransport returns the process transport used by the hidden attach
// command. It is exported for command wiring while tests use ProxyTransport.
func NewCommandTransport(remote config.RemoteConfig) ProxyTransport {
	return commandProxyTransport{config: remote}
}

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ ProxyTransport = commandProxyTransport{}

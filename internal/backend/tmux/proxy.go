package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/proxy"
)

const (
	proxySessionOption      = "@nexo-proxy"
	healthyAttachmentPeriod = proxy.HealthyAttachmentPeriod
)

// ProxyTransport is retained as the tmux adapter's compatibility alias for
// the backend-neutral attachment transport.
type ProxyTransport = proxy.Transport

// ProxyClock is retained as the tmux adapter's compatibility alias for the
// backend-neutral reconnect clock.
type ProxyClock = proxy.Clock

// RunReconnect is the backend-neutral reconnect loop used by tmux and Herdr.
var RunReconnect = proxy.RunReconnect

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

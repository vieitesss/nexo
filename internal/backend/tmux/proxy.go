package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

const proxySessionOption = "@nexo-proxy"

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
	b.remoteMachines = proxyidentity.NewMachineSet(remote.ConfiguredMachines())
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
	for _, r := range machine + container {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("tmux remote proxy names cannot contain control or format characters")
		}
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
	machine = strings.TrimSpace(machine)
	container = CanonicalContainerName(strings.TrimSpace(container))
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return "", err
	}
	identity := proxyidentity.NewIdentity(machine, container)
	return proxyidentity.NewPolicy(b.configuredMachines()).Resolve(ctx, identity, tmuxProxyStore{backend: b, includeLabels: b.isRemoteMachine(machine)})
}

// ProxyTarget returns the identity recorded on a local proxy session.
func (b *Backend) ProxyTarget(ctx context.Context, paneID string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("tmux remote proxy requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return "", errors.New("tmux proxy pane id is empty")
	}
	output, err := b.run(ctx, "display-message", "-p", "-t", paneID, "#{"+proxySessionOption+"}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// SendPaneKeys sends the original user binding to a local pane.
func (b *Backend) SendPaneKeys(ctx context.Context, paneID string, keys []string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux pane input requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("tmux pane input requires a pane id")
	}
	if len(keys) == 0 {
		return nil
	}
	args := make([]string, 0, len(keys)+3)
	args = append(args, "-t", paneID, "--")
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			return errors.New("tmux pane input requires a key")
		}
		args = append(args, tmuxParseKey(key))
	}
	_, err := b.run(ctx, "send-keys", args...)
	return err
}

// tmuxParseKey makes one send-keys operand safe for tmux's command parser.
// cmd_parse_from_arguments removes a trailing ";" from every argument and
// unescapes one preceding backslash; an unescaped trailing ";" ends the
// command and turns every following operand into a new tmux command. Only the
// final character is examined, so escaping that single semicolon keeps the
// key's literal meaning.
func tmuxParseKey(key string) string {
	if !strings.HasSuffix(key, ";") || strings.HasSuffix(key, `\;`) {
		return key
	}
	return key[:len(key)-1] + `\;`
}

// SendPaneText types literal text into a local pane.
func (b *Backend) SendPaneText(ctx context.Context, paneID, text string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux pane input requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("tmux pane input requires a pane id")
	}
	_, err := b.run(ctx, "send-keys", "-t", paneID, "-l", "--", tmuxParseKey(text))
	return err
}

func (b *Backend) configuredMachines() []string {
	if b == nil {
		return nil
	}
	return b.proxyConfig.ConfiguredMachines()
}

func (b *Backend) isRemoteMachine(machine string) bool {
	return b != nil && b.remoteMachines.Contains(machine)
}

type tmuxProxyStore struct {
	backend       *Backend
	includeLabels bool
}

func (s tmuxProxyStore) ListProxyCandidates(ctx context.Context) ([]proxyidentity.Candidate, error) {
	format := "#{session_id}\t#{@nexo-proxy}"
	if s.includeLabels {
		format = "#{session_id}\t#{session_name}\t#{@nexo-proxy}"
	}
	output, err := s.backend.run(ctx, "list-sessions", "-F", format)
	if err != nil {
		if isNoServer(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(output), "\r\n"), "\n")
	candidates := make([]proxyidentity.Candidate, 0, len(lines))
	for _, line := range lines {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		candidate := proxyidentity.Candidate{ID: fields[0], TaggedIdentity: fields[len(fields)-1]}
		if len(fields) == 3 {
			candidate.Label = canonicalProxyLabel(fields[1])
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s tmuxProxyStore) AdoptProxy(ctx context.Context, sessionID string, identity proxyidentity.Identity) error {
	_, err := s.backend.run(ctx, "set-option", "-t", sessionID, proxySessionOption, identity.String())
	return err
}

func canonicalProxyLabel(label string) string {
	identity, ok := proxyidentity.ParseValue(label)
	if !ok {
		return label
	}
	identity.Container = CanonicalContainerName(identity.Container)
	return identity.String()
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
	identity := proxyidentity.NewIdentity(machine, container)
	machine = identity.Machine

	name := identity.String()
	if _, err := b.proxyPaneCommand(machine, container); err != nil {
		return err
	}
	sessionID, err := b.ProxySessionID(ctx, machine, container)
	if err != nil {
		return err
	}
	if sessionID == "" {
		command, err := b.proxyPaneCommand(machine, container)
		if err != nil {
			return err
		}
		output, err := b.run(ctx, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", EscapeSessionName(name), command)
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
		if _, err := b.run(ctx, "set-option", "-t", sessionID, proxySessionOption, identity.String()); err != nil {
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
		_, err = b.runClient(ctx, "switch-client", "-t", sessionID)
	} else {
		err = b.runInteractive(ctx, "attach-session", "-t", sessionID)
	}
	if err != nil {
		return err
	}
	return b.recordTarget(ctx, sessionID)
}

func (b *Backend) proxyPaneCommand(machine, container string) (string, error) {
	return proxy.AttachCommand(proxyBinary(), "tmux", b.proxyConfigPath, machine, container, false)
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
// command.
func NewCommandTransport(remote config.RemoteConfig) proxy.Transport {
	return commandProxyTransport{config: remote}
}

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ capabilities.RemotePaste = (*Backend)(nil)
var _ proxy.Transport = commandProxyTransport{}

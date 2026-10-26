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
	container = proxy.CanonicalContainerName(strings.TrimSpace(container))
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return "", err
	}
	identity := proxyidentity.NewIdentity(machine, container)
	return proxyidentity.Resolve(ctx, identity, tmuxProxyStore{backend: b})
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

func (b *Backend) isRemoteMachine(machine string) bool {
	return b != nil && b.remoteMachines.Contains(machine)
}

type tmuxProxyStore struct {
	backend *Backend
}

func (s tmuxProxyStore) ListProxyCandidates(ctx context.Context) ([]proxyidentity.Candidate, error) {
	format := "#{session_id}\t#{@nexo-proxy}"
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
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// Proxy opens or switches to a local proxy container. The remote attachment
// is the only process in its pane; closing this local session cannot close the
// remote container.
func (b *Backend) Proxy(ctx context.Context, machine, container string) error {
	return b.openProxy(ctx, proxy.RemoteKindTmux, machine, proxy.CanonicalContainerName(strings.TrimSpace(container)))
}

// ProxyRemote opens or switches the machine proxy for a remote machine whose
// multiplexer is not tmux. The local container is an ordinary tmux proxy
// session; only its pane attaches to the remote Herdr client, so nexo never
// drives Herdr from tmux and needs no local Herdr install.
func (b *Backend) ProxyRemote(ctx context.Context, remoteKind, machine, container string) error {
	if remoteKind != proxy.RemoteKindHerdr {
		return fmt.Errorf("tmux remote proxy cannot attach to a %q remote", remoteKind)
	}
	return b.openProxy(ctx, remoteKind, machine, strings.TrimSpace(container))
}

func (b *Backend) openProxy(ctx context.Context, remoteKind, machine, container string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux remote proxy requires a command runner")
	}
	machine = strings.TrimSpace(machine)
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return err
	}
	identity := proxyidentity.NewIdentity(machine, "")
	machine = identity.Machine
	attachCommand := func() (string, error) {
		return proxy.AttachCommand(proxyBinary(), "tmux", b.proxyConfigPath, remoteKind, machine, container, false)
	}

	sessionID, err := b.ProxySessionID(ctx, machine, "")
	if err != nil {
		return err
	}
	if sessionID != "" {
		if container != "" {
			if switchErr := b.switchRemoteProxy(ctx, remoteKind, machine, container); switchErr != nil {
				if proxy.IsMissingRemoteTarget(switchErr) {
					if remoteKind == proxy.RemoteKindHerdr {
						return fmt.Errorf("no workspace %s on %s", container, machine)
					}
					return fmt.Errorf("no session %s on %s", container, machine)
				}
				command, commandErr := attachCommand()
				if commandErr != nil {
					return commandErr
				}
				if _, respawnErr := b.run(ctx, "respawn-pane", "-k", "-t", sessionID, command); respawnErr != nil {
					return errors.Join(switchErr, respawnErr)
				}
			}
		}
	} else {
		name, nameErr := b.proxySessionName(ctx, machine)
		if nameErr != nil {
			return nameErr
		}
		command, commandErr := attachCommand()
		if commandErr != nil {
			return commandErr
		}
		output, createErr := b.run(ctx, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", EscapeSessionName(name), command)
		if createErr != nil {
			return createErr
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
	if _, err := b.run(ctx, "set-option", "-t", sessionID, "detach-on-destroy", "off"); err != nil {
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

func (b *Backend) proxySessionName(ctx context.Context, machine string) (string, error) {
	output, err := b.run(ctx, "list-sessions", "-F", "#{session_name}\t#{@nexo-proxy}")
	if err != nil && !isNoServer(err) {
		return "", err
	}
	// tmux rewrites '.' and ':' when it creates a session, so occupancy must
	// be compared in tmux's canonical spelling, including suffixed candidates.
	used := make(map[string]bool)
	machineNameUsed := false
	canonicalMachine := proxy.CanonicalContainerName(machine)
	for _, line := range strings.Split(strings.TrimRight(string(output), "\r\n"), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) > 0 && fields[0] != "" {
			name := proxy.CanonicalContainerName(fields[0])
			used[name] = true
			if strings.EqualFold(name, canonicalMachine) {
				machineNameUsed = true
			}
		}
	}
	if !machineNameUsed {
		return canonicalMachine, nil
	}
	for suffix := 1; ; suffix++ {
		candidate := fmt.Sprintf("%s~remote", machine)
		if suffix > 1 {
			candidate = fmt.Sprintf("%s~remote%d", machine, suffix)
		}
		candidate = proxy.CanonicalContainerName(candidate)
		if !used[candidate] {
			return candidate, nil
		}
	}
}

func (b *Backend) switchRemoteProxy(ctx context.Context, remoteKind, machine, container string) error {
	if remoteKind == proxy.RemoteKindHerdr {
		_, err := b.Runner.Run(ctx, "ssh", proxy.HerdrSwitchSSHArgs(b.proxyConfig, machine, container)...)
		return err
	}
	_, err := b.Runner.Run(ctx, "ssh", proxy.RemoteSwitchSSHArgs(b.proxyConfig, "tmux", machine, container)...)
	return err
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

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ capabilities.RemoteKindProxy = (*Backend)(nil)
var _ capabilities.RemotePaste = (*Backend)(nil)
var _ capabilities.RemoteConfigurator = (*Backend)(nil)
var _ capabilities.ProxySessionResolver = (*Backend)(nil)

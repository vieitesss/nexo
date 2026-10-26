package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

// proxyShell runs the shared hidden attach command. proxy.AttachCommand builds
// a POSIX shell command, so the proxy block executes it through a non-login
// shell: Rex interpolates nothing, and `exec` makes the attachment the block's
// only process.
const proxyShell = "/bin/sh"

// validateProxyIdentity rejects a machine Rex cannot carry as proxy metadata.
// The session label is display data, so its 128-character limit does not bind
// the identity; control characters and a leading '-' still must not reach a
// label, a shell command, or the sidecar document.
func validateProxyIdentity(machine string) error {
	if strings.TrimSpace(machine) == "" {
		return errors.New("rex remote proxy requires a machine")
	}
	if strings.HasPrefix(machine, "-") {
		return fmt.Errorf("machine %q starts with '-'", machine)
	}
	for _, r := range machine {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("rex remote proxy machine cannot contain control or format characters")
		}
	}
	return nil
}

// ConfigureRemoteConfig supplies the remote settings and the optional explicit
// config path the attachment forwards to its child process.
func (b *Backend) ConfigureRemoteConfig(remote config.RemoteConfig, configPath string) {
	if b == nil {
		return
	}
	b.proxyConfig = remote
	b.proxyConfigPath = configPath
}

// ProxySessionID resolves the one machine proxy through nexo's sidecar
// metadata. A session label that happens to spell a machine never establishes
// identity, and a record for a session the server no longer has resolves to
// nothing. Proxy identity is machine-only, so a MACHINE/CONTAINER target (the
// documented spelling for close) still resolves that machine's proxy.
func (b *Backend) ProxySessionID(ctx context.Context, machine, container string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("rex remote proxy requires a command runner")
	}
	if err := validateProxyIdentity(machine); err != nil {
		return "", err
	}
	return proxyidentity.Resolve(ctx, proxyidentity.NewIdentity(machine, ""), rexProxyStore{backend: b})
}

// CurrentProxyMachine reports the machine the calling session's proxy attaches
// to, or nothing when the caller is in an ordinary session.
func (b *Backend) CurrentProxyMachine(ctx context.Context) (string, error) {
	if b == nil || b.Runner == nil {
		return "", nil
	}
	resolved, err := b.server(ctx)
	if err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(resolved.SessionID)
	if sessionID == "" {
		return "", nil
	}
	store, err := b.metadata(ctx)
	if err != nil {
		return "", err
	}
	record, ok, err := store.lookupProxy(sessionID)
	if err != nil || !ok {
		return "", err
	}
	return proxyidentity.NormalizeMachineName(record.Machine), nil
}

// Proxy opens or focuses one local Rex session per machine whose only block
// attaches to that machine's remote multiplexer. The remote is tmux or Herdr,
// which the shared attach command reaches through its own client; remote Rex is
// not a terminal attachment and is refused rather than guessed at.
func (b *Backend) Proxy(ctx context.Context, remoteKind, machine, container string) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex remote proxy requires a command runner")
	}
	switch remoteKind {
	case proxy.RemoteKindTmux, proxy.RemoteKindHerdr:
	default:
		return fmt.Errorf("backend %q cannot attach to a remote %q machine", "rex", remoteKind)
	}
	machine = proxyidentity.NormalizeMachineName(machine)
	if err := validateProxyIdentity(machine); err != nil {
		return err
	}
	// tmux rewrites '.' and ':' when it creates a session, so its targets are
	// canonicalized at this seam. A Herdr workspace ID is opaque and must reach
	// the remote client exactly as given.
	container = strings.TrimSpace(container)
	if remoteKind != proxy.RemoteKindHerdr {
		container = proxy.CanonicalContainerName(container)
	}

	// Lookup, creation, and recording are one transaction per machine: two
	// invocations opening the same machine must not both observe absence. The
	// existence check runs again under the lock rather than trusting a read
	// from before it.
	store, err := b.metadata(ctx)
	if err != nil {
		return err
	}
	unlock, err := store.lockProxy(machine)
	if err != nil {
		return err
	}
	defer unlock()

	sessionID, err := b.ProxySessionID(ctx, machine, "")
	if err != nil {
		return err
	}
	if sessionID != "" {
		return b.retargetProxy(ctx, sessionID, remoteKind, machine, container)
	}
	return b.openProxy(ctx, remoteKind, machine, container)
}

// openProxy creates the machine proxy and records its identity before the app
// is asked to show it. A session nexo cannot record is destroyed instead of
// left behind, so a retry cannot create a second proxy for the same machine.
func (b *Backend) openProxy(ctx context.Context, remoteKind, machine, container string) error {
	store, err := b.metadata(ctx)
	if err != nil {
		return err
	}
	sessionID, err := b.createProxySession(ctx, remoteKind, machine, container)
	if err != nil {
		return err
	}
	if err := store.recordProxy(sessionID, proxyRecord{Machine: machine, RemoteKind: remoteKind}); err != nil {
		return errors.Join(err, b.destroyProxySession(ctx, sessionID))
	}
	return b.focusAndRecord(ctx, sessionInfo{ID: sessionID}, false)
}

// retargetProxy switches the remote client of the existing machine proxy to an
// explicit container, then focuses the proxy. It never creates, restarts, or
// closes the remote container, and it never changes the kind the block is
// attached to: the recorded kind is the block's real multiplexer, so switching
// it would leave the record lying about what the pane runs.
func (b *Backend) retargetProxy(ctx context.Context, sessionID, remoteKind, machine, container string) error {
	store, err := b.metadata(ctx)
	if err != nil {
		return err
	}
	record, ok, err := store.lookupProxy(sessionID)
	if err != nil {
		return err
	}
	attached := proxy.RemoteKindTmux
	if ok && record.RemoteKind != "" {
		attached = record.RemoteKind
	}
	if attached != remoteKind {
		return fmt.Errorf("proxy for %s is attached to %s, not %s; run nexo remote close %s and open it again", machine, attached, remoteKind, machine)
	}
	if container != "" {
		if err := b.switchRemoteProxy(ctx, remoteKind, machine, container); err != nil {
			if proxy.IsMissingRemoteTarget(err) {
				if remoteKind == proxy.RemoteKindHerdr {
					return fmt.Errorf("no workspace %s on %s", container, machine)
				}
				return fmt.Errorf("no session %s on %s", container, machine)
			}
			return err
		}
	}
	return b.focusAndRecord(ctx, sessionInfo{ID: sessionID}, false)
}

// createProxySession creates one single-block session. The block runs the
// shared attach command directly, so no shell profile, alias, or interpolation
// can change what the proxy attaches to.
func (b *Backend) createProxySession(ctx context.Context, remoteKind, machine, container string) (string, error) {
	command, err := proxy.AttachCommand(proxy.ExecutablePath(), "rex", b.proxyConfigPath, remoteKind, machine, container, false)
	if err != nil {
		return "", err
	}
	login := false
	output, err := b.api(ctx, "session.create", createInput{
		Label: truncateLabel(machine),
		InitialWindows: []createWindow{{
			Layout: createLayout{Block: createBlock{
				Flavor: shellFlavor,
				Options: createOptions{
					Shell:   "none",
					Login:   &login,
					Command: []string{proxyShell, "-c", command},
				},
			}},
		}},
	})
	if err != nil {
		return "", err
	}
	var created createResult
	if err := json.Unmarshal(output, &created); err != nil {
		return "", fmt.Errorf("decode rex session.create: %w", err)
	}
	if strings.TrimSpace(created.SessionID) == "" {
		return "", errors.New("rex session.create returned no session id")
	}
	return created.SessionID, nil
}

func (b *Backend) switchRemoteProxy(ctx context.Context, remoteKind, machine, container string) error {
	_, err := b.Runner.Run(ctx, "ssh", proxy.SwitchSSHArgs(b.proxyConfig, "rex", remoteKind, machine, container)...)
	return err
}

func (b *Backend) destroyProxySession(ctx context.Context, sessionID string) error {
	return b.KillContainer(ctx, sessionID)
}

// rexProxyStore serves proxy lookup from the sidecar document, filtered to the
// sessions the live server actually has.
type rexProxyStore struct{ backend *Backend }

func (s rexProxyStore) ListProxyCandidates(ctx context.Context) ([]proxyidentity.Candidate, error) {
	store, err := s.backend.metadata(ctx)
	if err != nil {
		return nil, err
	}
	proxies, err := store.proxies()
	if err != nil {
		return nil, err
	}
	if len(proxies) == 0 {
		return nil, nil
	}
	sessions, err := s.backend.sessions(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]proxyidentity.Candidate, 0, len(proxies))
	for _, session := range sessions {
		record, ok := proxies[session.ID]
		if !ok || strings.TrimSpace(record.Machine) == "" {
			continue
		}
		candidates = append(candidates, proxyidentity.Candidate{
			ID:             session.ID,
			Label:          session.Label,
			TaggedIdentity: record.Machine,
		})
	}
	return candidates, nil
}

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ capabilities.RemoteConfigurator = (*Backend)(nil)
var _ capabilities.ProxySessionResolver = (*Backend)(nil)
var _ capabilities.CurrentProxyMachine = (*Backend)(nil)

package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/proxy"
	"github.com/vieitesss/nexo/internal/proxyidentity"
)

const proxyToken = "nexo-proxy"

// ConfigureRemoteConfig supplies the optional explicit config path forwarded to
// the child process in a proxy pane.
func (b *Backend) ConfigureRemoteConfig(remote config.RemoteConfig, configPath string) {
	if b == nil {
		return
	}
	b.proxyConfig = remote
	b.proxyConfigPath = configPath
	b.remoteMachines = proxyidentity.NewMachineSet(remote.ConfiguredMachines())
}

// ValidateProxyIdentity rejects identity values that Herdr would normalize,
// truncate, or interpret as terminal input. Validation deliberately happens
// before creating anything so every stored token is exact.
func ValidateProxyIdentity(machine, container string) error {
	if machine == "" {
		return errors.New("herdr remote proxy requires a machine")
	}
	if machine != strings.TrimSpace(machine) {
		return errors.New("herdr remote proxy machine cannot have leading or trailing whitespace")
	}
	if strings.HasPrefix(machine, "-") {
		return fmt.Errorf("machine %q starts with '-'", machine)
	}
	if container != strings.TrimSpace(container) {
		return errors.New("herdr remote proxy container cannot have leading or trailing whitespace")
	}
	// Herdr stores only the machine identity in workspace metadata; the
	// explicit container is typed into the pane command and never crosses the
	// 80-rune metadata limit.
	if utf8.RuneCountInString(machine) > 80 {
		return errors.New("herdr remote proxy machine cannot exceed 80 characters")
	}
	if hasControlOrFormat(machine + "/" + container) {
		return errors.New("herdr remote proxy names cannot contain control or format characters")
	}
	return nil
}

func hasControlOrFormat(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

// ProxySessionID resolves a machine proxy, while still accepting an explicit
// legacy machine/container identity for close and migration-free readers.
func (b *Backend) ProxySessionID(ctx context.Context, machine, container string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("herdr remote proxy requires a command runner")
	}
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return "", err
	}
	identity := proxyidentity.NewIdentity(machine, container)
	return proxyidentity.Resolve(ctx, identity, herdrProxyStore{backend: b})
}

// Proxy opens or focuses a local Herdr workspace whose pane runs the shared
// hidden attachment command. The metadata token is the stable identity.
//
// The only remote kind this pane can reach is tmux. A remote Herdr machine is
// reached through Herdr's own machine list (openHerdr), not through a proxy
// pane, so it is refused here rather than silently attaching to a tmux server
// that is not running.
func (b *Backend) Proxy(ctx context.Context, remoteKind, machine, container string) error {
	if remoteKind != proxy.RemoteKindTmux {
		return fmt.Errorf("backend %q cannot attach to a remote Herdr machine", "herdr")
	}
	if b == nil || b.Runner == nil {
		return errors.New("herdr remote proxy requires a command runner")
	}
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return err
	}
	identity := proxyidentity.NewIdentity(machine, "")
	machine = identity.Machine
	workspaceID, err := b.ProxySessionID(ctx, machine, "")
	if err != nil {
		return err
	}
	if workspaceID != "" {
		if container != "" {
			if switchErr := b.switchRemoteProxy(ctx, machine, container); switchErr != nil {
				if proxy.IsMissingRemoteTarget(switchErr) {
					return fmt.Errorf("no session %s on %s", container, machine)
				}
				workspaceID, err = b.restartProxyPane(ctx, workspaceID, machine, container)
				if err != nil {
					return errors.Join(switchErr, err)
				}
			}
		}
		if _, err := b.run(ctx, "workspace", "focus", workspaceID); err != nil {
			return err
		}
		return b.recordContainer(containerstate.Entry{ID: workspaceID, Machine: machine, Proxy: true})
	}

	workspaceID, err = b.createProxyWorkspace(ctx, machine, container)
	if err != nil {
		return err
	}
	if _, err := b.run(ctx, "workspace", "focus", workspaceID); err != nil {
		return err
	}
	return b.recordContainer(containerstate.Entry{ID: workspaceID, Machine: machine, Proxy: true})
}

func (b *Backend) createProxyWorkspace(ctx context.Context, machine, initial string) (string, error) {
	identityValue := proxyidentity.NewIdentity(machine, "").String()
	output, err := b.run(ctx, "workspace", "create", "--label", identityValue,
		"--env", "NEXO_PROXY_MACHINE="+machine, "--focus")
	if err != nil {
		return "", err
	}
	workspaceID, paneID := decodeCreatedWorkspace(output)
	if workspaceID == "" || paneID == "" {
		failure := errors.New("herdr remote proxy did not report a workspace and pane id")
		if workspaceID != "" {
			return "", b.operationWithCleanup(failure, workspaceID)
		}
		return "", failure
	}
	if _, err := b.run(ctx, "workspace", "report-metadata", workspaceID, "--source", "nexo", "--token", proxyToken+"="+identityValue); err != nil {
		return "", b.operationWithCleanup(err, workspaceID)
	}
	if err := b.verifyProxyToken(ctx, workspaceID, identityValue); err != nil {
		return "", b.operationWithCleanup(err, workspaceID)
	}
	command, err := proxy.AttachCommand(proxy.ExecutablePath(), "herdr", b.proxyConfigPath, proxy.RemoteKindTmux, "", initial, true)
	if err != nil {
		return "", b.operationWithCleanup(err, workspaceID)
	}
	if _, err := b.run(ctx, "pane", "send-text", paneID, command+"\n"); err != nil {
		return "", b.operationWithCleanup(err, workspaceID)
	}
	return workspaceID, nil
}

func (b *Backend) switchRemoteProxy(ctx context.Context, machine, container string) error {
	_, err := b.Runner.Run(ctx, "ssh", proxy.SwitchSSHArgs(b.proxyConfig, "herdr", proxy.RemoteKindTmux, machine, container)...)
	return err
}

func (b *Backend) restartProxyPane(ctx context.Context, workspaceID, machine, container string) (string, error) {
	if err := b.closeWorkspace(workspaceID); err != nil {
		return "", err
	}
	return b.createProxyWorkspace(ctx, machine, container)
}

func (b *Backend) verifyProxyToken(ctx context.Context, workspaceID, identity string) error {
	workspaces, err := b.listWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("verify proxy workspace token: %w", err)
	}
	for _, workspace := range workspaces {
		if workspace.ID == workspaceID && workspace.Tokens[proxyToken] == identity {
			return nil
		}
	}
	return errors.New("herdr remote proxy token did not retain its exact identity")
}

const proxyCleanupTimeout = 5 * time.Second

func (b *Backend) operationWithCleanup(operationErr error, workspaceID string) error {
	cleanupErr := b.closeWorkspace(workspaceID)
	if cleanupErr != nil {
		return fmt.Errorf("%w (also failed to close proxy workspace %s: %v)", operationErr, workspaceID, cleanupErr)
	}
	return operationErr
}

func (b *Backend) closeWorkspace(workspaceID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), proxyCleanupTimeout)
	defer cancel()
	return b.closeWorkspaceContext(cleanupCtx, workspaceID)
}

func (b *Backend) closeWorkspaceContext(ctx context.Context, workspaceID string) error {
	_, err := b.run(ctx, "workspace", "close", workspaceID)
	return err
}

// CloseProxyWorkspace closes the local Herdr workspace that owns paneID after
// a proxy attachment exits. It never sends a close operation to the remote
// machine.
func (b *Backend) CloseProxyWorkspace(ctx context.Context, paneID string) error {
	if b == nil || b.Runner == nil {
		return errors.New("close Herdr proxy workspace requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("herdr proxy pane id is required to close the workspace")
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, proxyCleanupTimeout)
	defer cancel()
	output, err := b.run(cleanupCtx, "pane", "get", paneID)
	if err != nil {
		return fmt.Errorf("find Herdr proxy workspace: %w", err)
	}
	workspaceID, err := decodePaneWorkspace(output)
	if err != nil || strings.TrimSpace(workspaceID) == "" {
		return errors.New("herdr proxy pane did not report a workspace id")
	}
	if err := b.closeWorkspaceContext(cleanupCtx, workspaceID); err != nil {
		return fmt.Errorf("close Herdr proxy workspace %s: %w", workspaceID, err)
	}
	return nil
}

// SendPaneKeys sends the original user binding to a Herdr pane through the
// native key-input command so key names keep their terminal semantics.
func (b *Backend) SendPaneKeys(ctx context.Context, paneID string, keys []string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr pane input requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("herdr pane input requires a pane id")
	}
	if len(keys) == 0 {
		return nil
	}
	args := make([]string, 0, len(keys)+3)
	args = append(args, "send-keys", paneID, "--")
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			return errors.New("herdr pane input requires a key")
		}
		args = append(args, key)
	}
	_, err := b.run(ctx, "pane", args...)
	return err
}

// SendPaneText types literal text into a Herdr pane.
func (b *Backend) SendPaneText(ctx context.Context, paneID, text string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr pane input requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("herdr pane input requires a pane id")
	}
	_, err := b.run(ctx, "pane", "send-text", paneID, text)
	return err
}

// ProxyTarget returns the remote identity attached by the current Herdr pane.
func (b *Backend) ProxyTarget(ctx context.Context, paneID string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("herdr remote proxy requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return "", errors.New("herdr proxy pane id is empty")
	}
	output, err := b.run(ctx, "pane", "get", paneID)
	if err != nil {
		return "", err
	}
	workspaceID, err := decodePaneWorkspace(output)
	if err != nil {
		return "", err
	}
	workspaces, err := b.listWorkspaces(ctx)
	if err != nil {
		return "", err
	}
	for _, workspace := range workspaces {
		if workspace.ID != workspaceID {
			continue
		}
		if identityValue := strings.TrimSpace(workspace.Tokens[proxyToken]); identityValue != "" {
			if identity, ok := proxyidentity.ParseValue(identityValue); ok && b.remoteMachines.Contains(identity.Machine) {
				return proxyidentity.NewIdentity(identity.Machine, identity.Container).String(), nil
			}
			return identityValue, nil
		}
		return "", nil
	}
	return "", nil
}

func (b *Backend) listWorkspaces(ctx context.Context) ([]workspace, error) {
	output, err := b.run(ctx, "workspace", "list")
	if err != nil {
		return nil, err
	}
	return decodeWorkspaceList(output)
}

type herdrProxyStore struct{ backend *Backend }

func (s herdrProxyStore) ListProxyCandidates(ctx context.Context) ([]proxyidentity.Candidate, error) {
	workspaces, err := s.backend.listWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]proxyidentity.Candidate, 0, len(workspaces))
	for _, workspace := range workspaces {
		candidates = append(candidates, proxyidentity.Candidate{
			ID:             workspace.ID,
			Label:          workspace.Label,
			TaggedIdentity: workspace.Tokens[proxyToken],
		})
	}
	return candidates, nil
}

func (b *Backend) proxyWorkspaceTarget(current workspace) (string, string, bool) {
	if identityValue := strings.TrimSpace(current.Tokens[proxyToken]); identityValue != "" {
		identity, ok := proxyidentity.ParseValue(identityValue)
		if !ok || !b.remoteMachines.Contains(identity.Machine) {
			return "", "", false
		}
		identity = proxyidentity.NewIdentity(identity.Machine, identity.Container)
		return identity.Machine, identity.Container, true
	}
	return "", "", false
}

func decodeCreatedWorkspace(output []byte) (workspaceID, paneID string) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	raw := output
	if json.Unmarshal(output, &envelope) == nil && len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return "", ""
	}
	return result.Workspace.ID, result.RootPane.ID
}

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ capabilities.RemoteConfigurator = (*Backend)(nil)
var _ capabilities.RemotePaste = (*Backend)(nil)
var _ capabilities.ProxySessionResolver = (*Backend)(nil)
var _ capabilities.ProxyWorkspaceCloser = (*Backend)(nil)

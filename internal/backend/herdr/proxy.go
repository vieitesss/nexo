package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	b.proxyConfigPath = configPath
	b.remoteMachines = proxyidentity.NewMachineSet(remote.ConfiguredMachines())
	b.proxyPolicy = proxyidentity.NewPolicy(remote.ConfiguredMachines())
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
	if container == "" {
		return errors.New("herdr remote proxy requires a container")
	}
	if container != strings.TrimSpace(container) {
		return errors.New("herdr remote proxy container cannot have leading or trailing whitespace")
	}
	identity := machine + "/" + container
	if utf8.RuneCountInString(identity) > 80 {
		return errors.New("herdr remote proxy identity cannot exceed 80 characters")
	}
	if hasControlOrFormat(identity) {
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

// ProxySessionID resolves a proxy by token, adopting an exact configured
// machine/container label when the token is absent.
func (b *Backend) ProxySessionID(ctx context.Context, machine, container string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("herdr remote proxy requires a command runner")
	}
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return "", err
	}
	identity := proxyidentity.NewIdentity(machine, container)
	return b.proxyPolicy.Resolve(ctx, identity, herdrProxyStore{backend: b})
}

func (b *Backend) adoptProxyWorkspace(ctx context.Context, workspaceID, identity string) error {
	if _, err := b.run(ctx, "workspace", "report-metadata", workspaceID, "--source", "nexo", "--token", proxyToken+"="+identity); err != nil {
		return fmt.Errorf("adopt proxy workspace %s: %w", workspaceID, err)
	}
	return b.verifyProxyToken(ctx, workspaceID, identity)
}

// Proxy opens or focuses a local Herdr workspace whose pane runs the shared
// hidden attachment command. The metadata token is the stable identity.
func (b *Backend) Proxy(ctx context.Context, machine, container string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr remote proxy requires a command runner")
	}
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return err
	}
	identity := proxyidentity.NewIdentity(machine, container)
	machine, container = identity.Machine, identity.Container
	command, err := b.proxyPaneCommand(machine, container)
	if err != nil {
		return err
	}
	workspaceID, err := b.ProxySessionID(ctx, machine, container)
	if err != nil {
		return err
	}
	if workspaceID != "" {
		if _, err := b.run(ctx, "workspace", "focus", workspaceID); err != nil {
			return err
		}
		return b.recordContainer(containerstate.Entry{ID: workspaceID, Name: container, Machine: machine, Proxy: true})
	}

	identityValue := identity.String()
	label := identityValue
	output, err := b.run(ctx, "workspace", "create", "--label", label,
		"--env", "NEXO_PROXY_MACHINE="+machine,
		"--env", "NEXO_PROXY_CONTAINER="+container, "--focus")
	if err != nil {
		return err
	}
	workspaceID, paneID := decodeCreatedWorkspace(output)
	if workspaceID == "" || paneID == "" {
		failure := errors.New("herdr remote proxy did not report a workspace and pane id")
		if workspaceID != "" {
			return b.operationWithCleanup(failure, workspaceID)
		}
		return failure
	}
	if _, err := b.run(ctx, "workspace", "report-metadata", workspaceID, "--source", "nexo", "--token", proxyToken+"="+identityValue); err != nil {
		return b.operationWithCleanup(err, workspaceID)
	}
	if err := b.verifyProxyToken(ctx, workspaceID, identityValue); err != nil {
		return b.operationWithCleanup(err, workspaceID)
	}
	if _, err := b.run(ctx, "pane", "send-text", paneID, command+"\n"); err != nil {
		return b.operationWithCleanup(err, workspaceID)
	}
	return b.recordContainer(containerstate.Entry{ID: workspaceID, Name: container, Machine: machine, Proxy: true})
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
		if machine, container, ok := b.proxyLabelTarget(workspace.Label); ok {
			identity := machine + "/" + container
			if err := b.adoptProxyWorkspace(ctx, workspace.ID, identity); err != nil {
				return "", err
			}
			return identity, nil
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

func (s herdrProxyStore) AdoptProxy(ctx context.Context, workspaceID string, identity proxyidentity.Identity) error {
	return s.backend.adoptProxyWorkspace(ctx, workspaceID, identity.String())
}

func (b *Backend) proxyLabelTarget(label string) (string, string, bool) {
	identity, ok := proxyidentity.ParseValue(label)
	if !ok || !b.remoteMachines.Contains(identity.Machine) {
		return "", "", false
	}
	identity = proxyidentity.NewIdentity(identity.Machine, identity.Container)
	return identity.Machine, identity.Container, true
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
	return b.proxyLabelTarget(current.Label)
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

func (b *Backend) proxyPaneCommand(_, _ string) (string, error) {
	binary := proxyBinary()
	return proxy.AttachCommand(binary, "herdr", b.proxyConfigPath, "", "", true)
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
var _ capabilities.RemoteConfigurator = (*Backend)(nil)
var _ capabilities.RemotePaste = (*Backend)(nil)
var _ capabilities.ProxySessionResolver = (*Backend)(nil)
var _ capabilities.ProxyWorkspaceCloser = (*Backend)(nil)

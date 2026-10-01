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
	"github.com/vieitesss/nexo/internal/proxy"
)

const proxyToken = "nexo-proxy"

// ConfigureRemoteConfig supplies the optional explicit config path forwarded to
// the child process in a proxy pane.
func (b *Backend) ConfigureRemoteConfig(remote config.RemoteConfig, configPath string) {
	if b == nil {
		return
	}
	b.proxyConfigPath = configPath
	b.remoteMachines = make(map[string]bool, len(remote.ConfiguredMachines()))
	for _, machine := range remote.ConfiguredMachines() {
		machine = config.NormalizeMachineName(machine)
		if machine != "" {
			b.remoteMachines[machine] = true
		}
	}
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
	machine = config.NormalizeMachineName(machine)
	workspaces, err := b.listWorkspaces(ctx)
	if err != nil {
		return "", err
	}
	identity := machine + "/" + container
	for _, workspace := range workspaces {
		if proxyIdentityMatches(workspace.Tokens[proxyToken], machine, container) {
			return workspace.ID, nil
		}
	}
	if b.remoteMachines[config.NormalizeMachineName(machine)] {
		for _, workspace := range workspaces {
			if !proxyIdentityMatches(workspace.Label, machine, container) {
				continue
			}
			if err := b.adoptProxyWorkspace(ctx, workspace.ID, identity); err != nil {
				return "", err
			}
			return workspace.ID, nil
		}
	}
	return "", nil
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
	machine = config.NormalizeMachineName(machine)
	command, err := b.proxyPaneCommand(machine, container)
	if err != nil {
		return err
	}
	workspaceID, err := b.ProxySessionID(ctx, machine, container)
	if err != nil {
		return err
	}
	if workspaceID != "" {
		_, err := b.run(ctx, "workspace", "focus", workspaceID)
		return err
	}

	identity := machine + "/" + container
	label := identity
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
	if _, err := b.run(ctx, "workspace", "report-metadata", workspaceID, "--source", "nexo", "--token", proxyToken+"="+identity); err != nil {
		return b.operationWithCleanup(err, workspaceID)
	}
	if err := b.verifyProxyToken(ctx, workspaceID, identity); err != nil {
		return b.operationWithCleanup(err, workspaceID)
	}
	if _, err := b.run(ctx, "pane", "send-text", paneID, command+"\n"); err != nil {
		return b.operationWithCleanup(err, workspaceID)
	}
	return nil
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
	_, err := b.run(cleanupCtx, "workspace", "close", workspaceID)
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
		if identity := strings.TrimSpace(workspace.Tokens[proxyToken]); identity != "" {
			machine, container, ok := splitProxyIdentity(identity)
			if ok && b.remoteMachines[config.NormalizeMachineName(machine)] {
				return config.NormalizeMachineName(machine) + "/" + container, nil
			}
			return identity, nil
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

func (b *Backend) proxyLabelTarget(label string) (string, string, bool) {
	separator := strings.IndexByte(label, '/')
	if separator <= 0 || separator == len(label)-1 {
		return "", "", false
	}
	machine, container := label[:separator], label[separator+1:]
	if !b.remoteMachines[config.NormalizeMachineName(machine)] {
		return "", "", false
	}
	return config.NormalizeMachineName(machine), container, true
}

func (b *Backend) proxyWorkspaceTarget(current workspace) (string, string, bool) {
	if identity := strings.TrimSpace(current.Tokens[proxyToken]); identity != "" {
		machine, container, ok := splitProxyIdentity(identity)
		if !ok || !b.remoteMachines[config.NormalizeMachineName(machine)] {
			return "", "", false
		}
		return config.NormalizeMachineName(machine), container, true
	}
	return b.proxyLabelTarget(current.Label)
}

func proxyIdentityMatches(identity, machine, container string) bool {
	identityMachine, identityContainer, ok := splitProxyIdentity(identity)
	return ok && config.NormalizeMachineName(identityMachine) == config.NormalizeMachineName(machine) && identityContainer == container
}

func splitProxyIdentity(identity string) (string, string, bool) {
	identity = strings.TrimSpace(identity)
	separator := strings.IndexByte(identity, '/')
	if separator <= 0 || separator == len(identity)-1 {
		return "", "", false
	}
	return strings.TrimSpace(identity[:separator]), strings.TrimSpace(identity[separator+1:]), true
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
var _ interface {
	ProxySessionID(context.Context, string, string) (string, error)
} = (*Backend)(nil)

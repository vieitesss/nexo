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

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

const proxyToken = "nexo-proxy"

// ConfigureRemoteConfig supplies proxy settings and the optional explicit
// config path forwarded to the child process in a proxy pane.
func (b *Backend) ConfigureRemoteConfig(remote config.RemoteConfig, configPath string) {
	if b == nil {
		return
	}
	b.proxyConfig = remote
	b.proxyConfigPath = configPath
}

// ValidateProxyIdentity rejects identity bytes that cannot safely be stored in
// Herdr metadata or passed to the hidden attach command.
func ValidateProxyIdentity(machine, container string) error {
	machine = strings.TrimSpace(machine)
	container = strings.TrimSpace(container)
	if machine == "" {
		return errors.New("herdr remote proxy requires a machine")
	}
	if strings.HasPrefix(machine, "-") {
		return fmt.Errorf("machine %q starts with '-'", machine)
	}
	if container == "" {
		return errors.New("herdr remote proxy requires a container")
	}
	if strings.ContainsAny(machine+container, "\x00\r\n\t") {
		return errors.New("herdr remote proxy names cannot contain NUL, newline, or tab")
	}
	return nil
}

// ProxySessionID resolves an exact proxy identity from Herdr's metadata token.
// Labels are display values and are deliberately not used for identity.
func (b *Backend) ProxySessionID(ctx context.Context, machine, container string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("herdr remote proxy requires a command runner")
	}
	if err := ValidateProxyIdentity(machine, container); err != nil {
		return "", err
	}
	workspaces, err := b.listWorkspaces(ctx)
	if err != nil {
		return "", err
	}
	identity := strings.TrimSpace(machine) + "/" + strings.TrimSpace(container)
	for _, workspace := range workspaces {
		if workspace.Tokens[proxyToken] == identity {
			return workspace.ID, nil
		}
	}
	return "", nil
}

// Proxy opens or focuses a local Herdr workspace whose pane runs the shared
// hidden attachment command. The metadata token is the stable identity.
func (b *Backend) Proxy(ctx context.Context, machine, container string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr remote proxy requires a command runner")
	}
	machine = strings.TrimSpace(machine)
	container = strings.TrimSpace(container)
	if err := ValidateProxyIdentity(machine, container); err != nil {
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
	output, err := b.run(ctx, "workspace", "create", "--label", label, "--focus")
	if err != nil {
		return err
	}
	workspaceID, paneID := decodeCreatedWorkspace(output)
	if workspaceID == "" || paneID == "" {
		if workspaceID != "" {
			_, _ = b.run(ctx, "workspace", "close", workspaceID)
		}
		return errors.New("herdr remote proxy did not report a workspace and pane id")
	}
	if _, err := b.run(ctx, "workspace", "report-metadata", workspaceID, "--source", "nexo", "--token", proxyToken+"="+identity); err != nil {
		_, _ = b.run(ctx, "workspace", "close", workspaceID)
		return err
	}
	command := b.proxyPaneCommand(machine, container)
	if _, err := b.run(ctx, "pane", "send-text", paneID, command+"\n"); err != nil {
		_, _ = b.run(ctx, "workspace", "close", workspaceID)
		return err
	}
	return nil
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
		if workspace.ID == workspaceID {
			return workspace.Tokens[proxyToken], nil
		}
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

func (b *Backend) proxyPaneCommand(machine, container string) string {
	binary := proxyBinary()
	parts := []string{shellQuote(binary), "--backend", "herdr"}
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
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

var _ capabilities.RemoteProxy = (*Backend)(nil)
var _ interface {
	ProxySessionID(context.Context, string, string) (string, error)
} = (*Backend)(nil)

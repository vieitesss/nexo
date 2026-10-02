package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

var (
	_ capabilities.ContainerLister  = (*Backend)(nil)
	_ capabilities.ContainerRenamer = (*Backend)(nil)
	_ capabilities.ContainerKiller  = (*Backend)(nil)
	_ capabilities.ScreenReader     = (*Backend)(nil)
)

type pane struct {
	ID        string `json:"pane_id"`
	Focused   bool   `json:"focused"`
	Workspace string `json:"workspace_id"`
}

// ListContainers maps Herdr's native workspaces to nexo containers and finds
// each workspace's focused pane for optional screen previews.
func (b *Backend) ListContainers(ctx context.Context) ([]capabilities.Container, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("herdr container lister requires a command runner")
	}
	output, err := b.run(ctx, "workspace", "list")
	if err != nil {
		return nil, err
	}
	workspaces, err := decodeWorkspaceList(output)
	if err != nil {
		return nil, err
	}
	machine := containerstate.LocalMachine()
	containers := make([]capabilities.Container, 0, len(workspaces))
	for _, workspace := range workspaces {
		proxyMachine, proxyContainer, isProxy := b.proxyWorkspaceTarget(workspace)
		if !isProxy && strings.TrimSpace(workspace.Tokens[proxyToken]) != "" {
			continue
		}
		paneID, paneErr := b.focusedPane(ctx, workspace.ID)
		if paneErr != nil {
			return nil, paneErr
		}
		if isProxy {
			containers = append(containers, capabilities.Container{
				ID:           workspace.ID,
				Name:         proxyContainer,
				Machine:      proxyMachine,
				ActivePaneID: paneID,
				Proxy:        true,
			})
			continue
		}
		containers = append(containers, capabilities.Container{
			ID:           workspace.ID,
			Name:         workspace.Label,
			Machine:      machine,
			ActivePaneID: paneID,
		})
	}
	return containers, nil
}

func (b *Backend) focusedPane(ctx context.Context, workspaceID string) (string, error) {
	output, err := b.run(ctx, "pane", "list", "--workspace", workspaceID)
	if err != nil {
		return "", nil
	}
	panes, err := decodePaneList(output)
	if err != nil {
		return "", nil
	}
	for _, current := range panes {
		if current.Focused {
			return current.ID, nil
		}
	}
	if len(panes) > 0 {
		return panes[0].ID, nil
	}
	return "", nil
}

// RenameContainer uses Herdr's native workspace rename operation.
func (b *Backend) RenameContainer(ctx context.Context, id, name string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr container renamer requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("herdr container renamer requires an id")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("herdr container renamer requires a name")
	}
	_, err := b.run(ctx, "workspace", "rename", id, name)
	return err
}

// KillContainer closes one selected Herdr workspace.
func (b *Backend) KillContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr container killer requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return nil
	}
	_, err := b.run(ctx, "workspace", "close", id)
	return err
}

// KillContainers closes the selected Herdr workspaces once the picker has
// confirmed the complete selection.
func (b *Backend) KillContainers(ctx context.Context, ids []string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr container killer requires a command runner")
	}
	current, err := b.currentContainer(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == current {
			continue
		}
		if err := b.KillContainer(ctx, id); err != nil {
			return err
		}
	}
	if current != "" {
		for _, id := range ids {
			if id == current {
				return b.KillContainer(ctx, id)
			}
		}
	}
	return nil
}

func (b *Backend) currentContainer(ctx context.Context) (string, error) {
	pane := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if pane == "" {
		return "", nil
	}
	output, err := b.run(ctx, "pane", "get", pane)
	if err != nil {
		return "", err
	}
	return decodePaneWorkspace(output)
}

// ReadScreen reads Herdr's native pane snapshot. Herdr owns the terminal
// protocol, so nexo asks it for visible text rather than scraping a socket.
func (b *Backend) ReadScreen(ctx context.Context, paneID string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("herdr screen reader requires a command runner")
	}
	if strings.TrimSpace(paneID) == "" {
		return "", errors.New("herdr screen reader requires a pane id")
	}
	output, err := b.run(ctx, "pane", "read", paneID, "--source", "visible")
	if err != nil {
		return "", err
	}
	return decodePaneText(output)
}

type herdrPaneList struct {
	Type  string `json:"type"`
	Panes []pane `json:"panes"`
}

func decodePaneList(output []byte) ([]pane, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode herdr pane list: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result herdrPaneList
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode herdr pane list: %w", err)
	}
	if result.Type != "" && result.Type != "pane_list" {
		return nil, fmt.Errorf("decode herdr pane list: unexpected result type %q", result.Type)
	}
	return result.Panes, nil
}

func decodePaneText(output []byte) (string, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return string(output), nil
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Text string `json:"text"`
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decode herdr pane read: %w", err)
	}
	if result.Read.Text != "" {
		return result.Read.Text, nil
	}
	return result.Text, nil
}

func decodePaneWorkspace(output []byte) (string, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return "", fmt.Errorf("decode herdr pane: %w", err)
	}
	var paneResult struct {
		Pane struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(envelope.Result, &paneResult); err != nil {
		return "", fmt.Errorf("decode herdr pane: %w", err)
	}
	return paneResult.Pane.WorkspaceID, nil
}

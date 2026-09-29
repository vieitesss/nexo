// Package herdr contains the Herdr backend adapter.
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
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/runner"
)

// Backend is the Herdr backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner
	agents *Source

	proxyConfigPath string
}

// New constructs a Herdr backend using the supplied command runner.
func New(r runner.CommandRunner) *Backend {
	return &Backend{Runner: r, agents: NewAgentSource(r)}
}

// OpenContainer focuses an existing Herdr workspace by stable workspace ID.
func (b *Backend) OpenContainer(ctx context.Context, id string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr container opener requires a command runner")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("herdr container opener requires an id")
	}
	_, err := b.run(ctx, "workspace", "focus", id)
	return err
}

// ListAgents implements capabilities.AgentSource with Herdr's native agent
// awareness.
func (b *Backend) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	if b == nil || b.agents == nil {
		return nil, errors.New("herdr agents requires a command runner")
	}
	return b.agents.ListAgents(ctx)
}

// FocusPane focuses the exact agent pane ID through Herdr's agent command.
// Herdr accepts pane IDs as agent targets and marks the pane seen as part of
// the native focus operation.
func (b *Backend) FocusPane(ctx context.Context, paneID string) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr pane focuser requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("herdr pane focuser requires a pane id")
	}
	_, err := b.run(ctx, "agent", "focus", paneID)
	return err
}

var _ capabilities.AgentSource = (*Backend)(nil)
var _ capabilities.PaneFocuser = (*Backend)(nil)

// Name identifies this backend.
func (Backend) Name() string { return "herdr" }

// Error reports a failed Herdr operation and preserves Herdr's stderr.
type Error struct {
	Operation string
	Args      []string
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("herdr %s: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("herdr %s: %v", e.Operation, e.Err)
}

// Unwrap preserves the command runner error for errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

func (b *Backend) CreateWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("herdr worktree creator requires a command runner")
	}
	path, err := filepath.Abs(options.Destination)
	if err != nil {
		return capabilities.Worktree{}, fmt.Errorf("resolve worktree destination: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return capabilities.Worktree{}, fmt.Errorf("worktree destination %q already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return capabilities.Worktree{}, fmt.Errorf("check worktree destination %q: %w", path, err)
	}

	args := []string{"worktree", "create", "--cwd", options.Repo, "--branch", options.Branch}
	if options.NewBranch {
		args = append(args, "--base", "HEAD")
	}
	args = append(args, "--path", path, "--focus")
	output, err := b.run(ctx, args[0], args[1:]...)
	if err != nil {
		return capabilities.Worktree{}, err
	}
	return capabilities.Worktree{
		Path:        path,
		Branch:      options.Branch,
		Repo:        options.Repo,
		NewBranch:   options.NewBranch,
		ContainerID: decodeWorktreeWorkspaceID(output),
	}, nil
}

// RemoveWorktree removes a Herdr worktree workspace created by this invocation.
func (b *Backend) RemoveWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr worktree remover requires a command runner")
	}
	if worktree.ContainerID == "" {
		return errors.New("herdr worktree result does not include a workspace id")
	}
	_, err := b.run(ctx, "worktree", "remove", "--workspace", worktree.ContainerID, "--force")
	return err
}

func decodeWorktreeWorkspaceID(output []byte) string {
	if len(output) == 0 {
		return ""
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(output, &envelope) != nil {
		return ""
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	return result.Workspace.ID
}

// OpenProject focuses an existing Herdr workspace selected by label, or
// creates and focuses a workspace for path when none is found.
func (b *Backend) OpenProject(ctx context.Context, path string, options capabilities.OpenOptions) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr project opener requires a command runner")
	}
	if options.ContainerID != "" {
		_, err := b.run(ctx, "workspace", "focus", options.ContainerID)
		return err
	}

	output, err := b.run(ctx, "workspace", "list")
	if err != nil {
		return err
	}
	workspaces, err := decodeWorkspaceList(output)
	if err != nil {
		return err
	}

	target := projects.TargetName(path)
	for _, workspace := range workspaces {
		if workspace.Label == target {
			_, err := b.run(ctx, "workspace", "focus", workspace.ID)
			return err
		}
	}

	directory := filepath.Base(filepath.Clean(path))
	for _, workspace := range workspaces {
		if strings.Contains(workspace.Label, directory) {
			_, err := b.run(ctx, "workspace", "focus", workspace.ID)
			return err
		}
	}

	_, err = b.run(ctx, "workspace", "create", "--cwd", path, "--label", target, "--focus")
	return err
}

type workspace struct {
	ID          string            `json:"workspace_id"`
	Label       string            `json:"label"`
	Tokens      map[string]string `json:"tokens"`
	Focused     bool              `json:"focused"`
	ActiveTabID string            `json:"active_tab_id"`
	PaneCount   int               `json:"pane_count"`
}

// decodeWorkspaceList accepts the JSON envelope emitted by the Herdr CLI and
// the result object directly, which keeps the command seam easy to fake.
func decodeWorkspaceList(output []byte) ([]workspace, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode herdr workspace list: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Type       string      `json:"type"`
		Workspaces []workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode herdr workspace list: %w", err)
	}
	if result.Type != "" && result.Type != "workspace_list" {
		return nil, fmt.Errorf("decode herdr workspace list: unexpected result type %q", result.Type)
	}
	return result.Workspaces, nil
}

func (b *Backend) run(ctx context.Context, operation string, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "herdr", append([]string{operation}, args...)...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		stderr = string(exitErr.Stderr)
	}
	if stderr == "" {
		stderr = err.Error()
	}
	return nil, &Error{
		Operation: operation,
		Args:      append([]string(nil), args...),
		Stderr:    stderr,
		Err:       err,
	}
}

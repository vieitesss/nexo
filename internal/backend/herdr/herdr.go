// Package herdr contains the Herdr backend adapter.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

// New constructs a Herdr backend using the supplied command runner.
func New(r runner.CommandRunner) *Backend {
	return &Backend{Runner: r}
}

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
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
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

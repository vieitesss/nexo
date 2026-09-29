// Package tmux contains the tmux backend adapter.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/runner"
)

// Backend is the tmux backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner
	Inside bool
	agents *Source
}

// New constructs a tmux backend using the supplied command runner. The
// optional argument identifies whether nexo is running inside tmux.
func New(r runner.CommandRunner, inside ...bool) *Backend {
	inTmux := os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
	if len(inside) > 0 {
		inTmux = inside[0]
	}
	return &Backend{Runner: r, Inside: inTmux, agents: NewAgentSource(r)}
}

// Name identifies this backend.
func (Backend) Name() string { return "tmux" }

// Error reports a failed tmux operation and preserves tmux's stderr.
type Error struct {
	Operation string
	Args      []string
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("tmux %s: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("tmux %s: %v", e.Operation, e.Err)
}

// Unwrap preserves the command runner error for errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// UnsupportedError reports a project-opening option that tmux does not
// support.
type UnsupportedError struct {
	Option string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("tmux does not support %s", e.Option)
}

func (b *Backend) CreateWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("tmux worktree creator requires a command runner")
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

	branchExisted := false
	if options.NewBranch {
		branchExisted = b.branchExists(ctx, options.Repo, options.Branch)
		if branchExisted {
			return capabilities.Worktree{}, fmt.Errorf("branch %q already exists", options.Branch)
		}
	}
	args := []string{"-C", options.Repo, "worktree", "add"}
	if options.NewBranch {
		args = append(args, "-b", options.Branch, path)
	} else {
		args = append(args, path, options.Branch)
	}
	if _, err := b.runGit(ctx, args...); err != nil {
		rollbackErr := b.rollbackWorktree(ctx, options.Repo, path, options.NewBranch, options.Branch, branchExisted)
		if rollbackErr != nil {
			return capabilities.Worktree{}, errors.Join(err, rollbackErr)
		}
		return capabilities.Worktree{}, err
	}

	result := capabilities.Worktree{Path: path, Branch: options.Branch, Repo: options.Repo, NewBranch: options.NewBranch}
	createdSession, err := b.openProject(ctx, path, capabilities.OpenOptions{})
	if createdSession {
		result.SessionName = projects.TargetName(path)
	}
	if err != nil {
		rollbackErr := b.rollbackWorktree(ctx, options.Repo, path, options.NewBranch, options.Branch, branchExisted)
		if createdSession {
			if _, killErr := b.run(ctx, "kill-session", "-t", "="+projects.TargetName(path)); killErr != nil {
				rollbackErr = errors.Join(rollbackErr, killErr)
			}
		}
		if rollbackErr != nil {
			return capabilities.Worktree{}, errors.Join(err, rollbackErr)
		}
		return capabilities.Worktree{}, err
	}
	return result, nil
}

func (b *Backend) RemoveWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux worktree remover requires a command runner")
	}
	var failures []error
	if worktree.SessionName != "" {
		if _, err := b.run(ctx, "kill-session", "-t", "="+worktree.SessionName); err != nil {
			failures = append(failures, err)
		}
	}
	path, err := filepath.Abs(worktree.Path)
	if err != nil {
		failures = append(failures, fmt.Errorf("resolve worktree destination: %w", err))
	} else if _, err := b.runGit(ctx, "-C", worktree.Repo, "worktree", "remove", "--force", path); err != nil {
		failures = append(failures, err)
	}
	if worktree.NewBranch {
		if _, err := b.runGit(ctx, "-C", worktree.Repo, "branch", "-D", worktree.Branch); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (b *Backend) rollbackWorktree(ctx context.Context, repo, path string, newBranch bool, branch string, branchExisted bool) error {
	var failures []error
	if _, err := os.Lstat(path); err == nil {
		if _, removeErr := b.runGit(ctx, "-C", repo, "worktree", "remove", "--force", path); removeErr != nil {
			failures = append(failures, removeErr)
		}
	}
	if newBranch && !branchExisted && b.branchExists(ctx, repo, branch) {
		if _, err := b.runGit(ctx, "-C", repo, "branch", "-D", branch); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (b *Backend) branchExists(ctx context.Context, repo, branch string) bool {
	_, err := b.runGit(ctx, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func (b *Backend) runGit(ctx context.Context, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "git", args...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	if stderr == "" {
		stderr = err.Error()
	}
	return nil, &Error{Operation: "git", Args: append([]string(nil), args...), Stderr: stderr, Err: err}
}

// OpenProject opens path in a tmux session and focuses that session.
func (b *Backend) OpenProject(ctx context.Context, path string, options capabilities.OpenOptions) error {
	_, err := b.openProject(ctx, path, options)
	return err
}

func (b *Backend) openProject(ctx context.Context, path string, options capabilities.OpenOptions) (bool, error) {
	if options.ContainerID != "" {
		return false, &UnsupportedError{Option: "--workspace-id"}
	}
	if b == nil || b.Runner == nil {
		return false, errors.New("tmux project opener requires a command runner")
	}

	name := projects.TargetName(path)
	_, err := b.run(ctx, "has-session", "-t", "="+name)
	exists := err == nil
	if err != nil && !isMissingSession(err) {
		return false, err
	}
	created := false
	if !exists {
		if _, err := b.run(ctx, "new-session", "-d", "-s", name, "-c", path); err != nil {
			return false, err
		}
		created = true
	}

	if b.Inside {
		_, err = b.run(ctx, "switch-client", "-t", "="+name)
		return created, err
	}
	return created, b.runInteractive(ctx, "attach-session", "-t", "="+name)
}

func (b *Backend) runInteractive(ctx context.Context, operation string, args ...string) error {
	interactive, ok := b.Runner.(runner.InteractiveRunner)
	if !ok {
		return &Error{
			Operation: operation,
			Args:      append([]string(nil), args...),
			Stderr:    "interactive command runner is unavailable",
			Err:       errors.New("runner does not support interactive commands"),
		}
	}
	if err := interactive.RunInteractive(ctx, "tmux", append([]string{operation}, args...)...); err != nil {
		stderr := interactiveStderr(err)
		if stderr == "" {
			stderr = err.Error()
		}
		return &Error{
			Operation: operation,
			Args:      append([]string(nil), args...),
			Stderr:    stderr,
			Err:       err,
		}
	}
	return nil
}

func (b *Backend) run(ctx context.Context, operation string, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "tmux", append([]string{operation}, args...)...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	if captured := interactiveStderr(err); captured != "" {
		stderr = captured
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

func interactiveStderr(err error) string {
	var interactiveErr *runner.InteractiveError
	if errors.As(err, &interactiveErr) && interactiveErr.Stderr != "" {
		return interactiveErr.Stderr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return string(exitErr.Stderr)
	}
	return ""
}

func isMissingSession(err error) bool {
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) {
		return false
	}
	message := strings.ToLower(tmuxErr.Stderr)
	return strings.Contains(message, "can't find session") ||
		strings.Contains(message, "session not found") ||
		strings.Contains(message, "no server running")
}

func sessionName(path string) string { return projects.TargetName(path) }

// ConfigureAgents applies the shared agents configuration to the tmux source.
func (b *Backend) ConfigureAgents(cfg config.AgentsConfig) {
	b.agents.ConfigureAgents(cfg)
}

// ListAgents implements capabilities.AgentSource.
func (b *Backend) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	return b.agents.ListAgents(ctx)
}

// MarkAgentSeen records focus entering one exact stopped pane.
func (b *Backend) MarkAgentSeen(ctx context.Context, paneID string) error {
	return b.agents.MarkSeen(ctx, paneID)
}

// WatchAgents runs the tmux agent poller until ctx is canceled.
func (b *Backend) WatchAgents(ctx context.Context) error {
	return b.agents.Watch(ctx)
}

// FocusPane switches to the exact tmux pane target. Each operation receives
// the pane ID so tmux resolves its owning container and window before the
// final pane selection.
func (b *Backend) FocusPane(ctx context.Context, paneID string) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux pane focuser requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("tmux pane focuser requires a pane id")
	}
	if b.Inside {
		for _, operation := range []string{"switch-client", "select-window", "select-pane"} {
			if _, err := b.run(ctx, operation, "-t", paneID); err != nil {
				return err
			}
		}
		return nil
	}
	for _, operation := range []string{"select-window", "select-pane"} {
		if _, err := b.run(ctx, operation, "-t", paneID); err != nil {
			return err
		}
	}
	return b.runInteractive(ctx, "attach-session", "-t", paneID)
}

var _ capabilities.AgentSource = (*Backend)(nil)
var _ capabilities.PaneFocuser = (*Backend)(nil)

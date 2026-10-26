package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/lifecycle"
)

// GitCommand runs one Git command and returns its standard output. Backends
// inject their own runner so failures keep each adapter's diagnostics.
type GitCommand func(context.Context, ...string) ([]byte, error)

// Worktrees owns the raw-Git half of the shared worktree transaction: branch
// preflight, the add command, compensation for a partially failed add, and the
// removal and conditional branch deletion a later rollback needs. Native
// container lifetime stays in each backend adapter.
type Worktrees struct {
	run GitCommand
}

// NewWorktrees returns the raw-Git worktree transaction over an injected Git
// command runner.
func NewWorktrees(run GitCommand) *Worktrees {
	return &Worktrees{run: run}
}

// Create preflights a new branch, adds the worktree and compensates
// immediately when Git fails. Whether the branch existed is private to this
// call: a branch this invocation did not create is never deleted.
func (w *Worktrees) Create(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	branchExisted := false
	if options.NewBranch {
		branchExisted = w.branchExists(ctx, options.Repo, options.Branch)
		if branchExisted {
			return capabilities.Worktree{}, fmt.Errorf("branch %q already exists", options.Branch)
		}
	}
	args := []string{"-C", options.Repo, "worktree", "add"}
	if options.NewBranch {
		args = append(args, "-b", options.Branch, options.Destination)
	} else {
		args = append(args, options.Destination, options.Branch)
	}
	if _, err := w.command(ctx, args...); err != nil {
		cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
		defer cancel()
		ownsBranch := options.NewBranch && !branchExisted
		return capabilities.Worktree{}, errors.Join(err, w.compensate(cleanupCtx, options.Repo, options.Destination, options.Branch, ownsBranch))
	}
	return capabilities.Worktree{Path: options.Destination, Branch: options.Branch, Repo: options.Repo, NewBranch: options.NewBranch}, nil
}

// Remove deletes a worktree this invocation created and, when it also created
// the branch, that branch. Native session or container cleanup stays in the
// caller, which decides whether it owns the container.
func (w *Worktrees) Remove(ctx context.Context, worktree capabilities.Worktree) error {
	var failures []error
	path, err := filepath.Abs(worktree.Path)
	if err != nil {
		failures = append(failures, fmt.Errorf("resolve worktree destination: %w", err))
	} else if _, err := w.command(ctx, "-C", worktree.Repo, "worktree", "remove", "--force", path); err != nil {
		failures = append(failures, err)
	}
	if worktree.NewBranch {
		if _, err := w.command(ctx, "-C", worktree.Repo, "branch", "-D", worktree.Branch); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Rollback compensates a worktree that was created but could not be opened.
// It deletes the branch the invocation created and tolerates an absent
// destination or branch instead of reporting what Git already removed.
func (w *Worktrees) Rollback(ctx context.Context, worktree capabilities.Worktree) error {
	return w.compensate(ctx, worktree.Repo, worktree.Path, worktree.Branch, worktree.NewBranch)
}

// compensate removes whatever a failed add left behind. Both steps are
// best-effort: a failure is collected so the original error stays visible.
func (w *Worktrees) compensate(ctx context.Context, repo, destination, branch string, ownsBranch bool) error {
	var failures []error
	if _, err := os.Lstat(destination); err == nil {
		if _, removeErr := w.command(ctx, "-C", repo, "worktree", "remove", "--force", destination); removeErr != nil {
			failures = append(failures, removeErr)
		}
	}
	if ownsBranch && w.branchExists(ctx, repo, branch) {
		if _, err := w.command(ctx, "-C", repo, "branch", "-D", branch); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// branchExists reports whether a local branch ref exists. A failed query counts
// as absent: the preflight only needs to know whether creating the branch is
// still safe.
func (w *Worktrees) branchExists(ctx context.Context, repo, branch string) bool {
	_, err := w.command(ctx, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func (w *Worktrees) command(ctx context.Context, args ...string) ([]byte, error) {
	if w == nil || w.run == nil {
		return nil, fmt.Errorf("git %s requires a command runner", strings.Join(args, " "))
	}
	return w.run(ctx, args...)
}

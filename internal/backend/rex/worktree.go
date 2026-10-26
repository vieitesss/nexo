package rex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/lifecycle"
)

var (
	_ capabilities.WorktreeCreator            = (*Backend)(nil)
	_ capabilities.WorktreeExtractor          = (*Backend)(nil)
	_ capabilities.WorktreeExtractionRollback = (*Backend)(nil)
	_ capabilities.WorktreeRemover            = (*Backend)(nil)
)

// CreateWorktree creates the worktree with Git and opens it in a session. The
// shared lifecycle owns destination preflight and cleanup sequencing; only the
// native worktree and session work lives here.
func (b *Backend) CreateWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("rex worktree creator requires a command runner")
	}
	return lifecycle.CreateWorktree(ctx, options, lifecycle.WorktreeAdapter{
		Create:   b.createWorktree,
		Open:     b.openProjectNative,
		Rollback: b.removeWorktree,
	})
}

// ExtractWorktree moves a branch out of the original checkout. The shared
// lifecycle performs the Git extraction and its rollback policy.
func (b *Backend) ExtractWorktree(ctx context.Context, options capabilities.WorktreeExtractOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("rex worktree extractor requires a command runner")
	}
	return lifecycle.ExtractWorktree(ctx, options, lifecycle.ExtractAdapter{
		WorktreeAdapter: lifecycle.WorktreeAdapter{
			Create:   b.createWorktree,
			Open:     b.openProjectNative,
			Rollback: b.removeWorktree,
		},
		Git: git.New(b.Runner),
	})
}

func (b *Backend) createWorktree(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	branchExisted := false
	if options.NewBranch {
		branchExisted = b.branchExists(ctx, options.Repo, options.Branch)
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
	if _, err := b.runGit(ctx, args...); err != nil {
		cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
		defer cancel()
		rollbackErr := b.rollbackWorktree(cleanupCtx, options.Repo, options.Destination, options.NewBranch, options.Branch, branchExisted)
		return capabilities.Worktree{}, errors.Join(err, rollbackErr)
	}
	return capabilities.Worktree{Path: options.Destination, Branch: options.Branch, Repo: options.Repo, NewBranch: options.NewBranch}, nil
}

// RollbackExtractWorktree removes the extracted worktree and restores its
// branch in the original checkout. Cleanup is independent of cancellation
// because it is part of the data-safety transaction.
func (b *Backend) RollbackExtractWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	cleanupCtx, cancel := lifecycle.CleanupContext(ctx)
	defer cancel()
	cleanupErr := b.removeWorktree(cleanupCtx, worktree)
	if worktree.Repo == "" || worktree.Branch == "" {
		return errors.Join(cleanupErr, errors.New("extracted worktree result lacks original branch identity"))
	}
	restoreErr := git.New(b.Runner).Checkout(cleanupCtx, worktree.Repo, worktree.Branch)
	return errors.Join(cleanupErr, restoreErr)
}

// RemoveWorktree removes a worktree and the session nexo created for it. An
// adopted session is left alone: the user still owns it.
func (b *Backend) RemoveWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex worktree remover requires a command runner")
	}
	return b.removeWorktree(ctx, worktree)
}

func (b *Backend) removeWorktree(ctx context.Context, worktree capabilities.Worktree) error {
	var failures []error
	if worktree.ContainerID != "" && worktree.Container.Created {
		if err := b.KillContainer(ctx, worktree.ContainerID); err != nil {
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
	return nil, &Error{Operation: "git", Stderr: stderr, Err: err}
}

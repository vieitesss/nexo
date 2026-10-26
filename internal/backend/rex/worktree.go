package rex

import (
	"context"
	"errors"

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
	return lifecycle.CreateWorktree(ctx, options, b.worktreeAdapter())
}

// ExtractWorktree moves a branch out of the original checkout. The shared
// lifecycle performs the Git extraction and its rollback policy.
func (b *Backend) ExtractWorktree(ctx context.Context, options capabilities.WorktreeExtractOptions) (capabilities.Worktree, error) {
	if b == nil || b.Runner == nil {
		return capabilities.Worktree{}, errors.New("rex worktree extractor requires a command runner")
	}
	return lifecycle.ExtractWorktree(ctx, options, lifecycle.ExtractAdapter{
		WorktreeAdapter: b.worktreeAdapter(),
		Git:             git.New(b.Runner),
	})
}

// worktreeAdapter binds the shared raw-Git transaction to Rex's native work:
// creation opens the worktree in a session, rollback destroys only the session
// nexo created and then removes the Git resources.
func (b *Backend) worktreeAdapter() lifecycle.WorktreeAdapter {
	return lifecycle.WorktreeAdapter{
		Create:   b.worktrees().Create,
		Open:     b.openProjectNative,
		Rollback: b.removeWorktree,
	}
}

func (b *Backend) worktrees() *git.Worktrees {
	return git.NewWorktrees(b.runGit)
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
	failures = append(failures, b.worktrees().Remove(ctx, worktree))
	return errors.Join(failures...)
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

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/vieitesss/nexo/internal/capabilities"
)

// WorktreeGit is the Git seam the extraction transaction needs. The concrete
// adapter lives in internal/git so this policy stays free of command strings.
type WorktreeGit interface {
	CurrentBranch(context.Context, string) (string, error)
	IsClean(context.Context, string) (bool, error)
	BranchOwners(context.Context, string, string) ([]string, error)
	PreviousBranch(context.Context, string) (string, error)
	DefaultBranch(context.Context, string) (string, error)
	BranchExists(context.Context, string, string) (bool, error)
	Checkout(context.Context, string, string) error
}

// ExtractAdapter is the native worktree seam for extraction. It reuses the
// shared create, open, and rollback operations and adds the Git transaction
// seam that owns the branch swap.
type ExtractAdapter struct {
	WorktreeAdapter
	Git WorktreeGit
}

// ExtractWorktree moves the branch currently checked out in the original
// worktree into a new worktree at the destination, leaving the original on a
// return branch. Every preflight check runs before the original is mutated.
func ExtractWorktree(ctx context.Context, options capabilities.WorktreeExtractOptions, adapter ExtractAdapter) (capabilities.Worktree, error) {
	if adapter.Git == nil {
		return capabilities.Worktree{}, errors.New("worktree extraction requires a git adapter")
	}
	if adapter.Create == nil {
		return capabilities.Worktree{}, errors.New("worktree extraction requires a create adapter")
	}
	repo, err := filepath.Abs(options.Repo)
	if err != nil {
		return capabilities.Worktree{}, fmt.Errorf("resolve original worktree: %w", err)
	}
	destination, err := preflightDestination(options.Destination)
	if err != nil {
		return capabilities.Worktree{}, err
	}

	branch, err := adapter.Git.CurrentBranch(ctx, repo)
	if err != nil {
		return capabilities.Worktree{}, err
	}
	if branch == "" {
		return capabilities.Worktree{}, fmt.Errorf("original worktree %q is not on a named branch", repo)
	}
	clean, err := adapter.Git.IsClean(ctx, repo)
	if err != nil {
		return capabilities.Worktree{}, err
	}
	if !clean {
		return capabilities.Worktree{}, fmt.Errorf("original worktree %q has uncommitted changes", repo)
	}
	if owner, err := checkedOutElsewhere(ctx, adapter.Git, repo, branch); err != nil {
		return capabilities.Worktree{}, err
	} else if owner != "" {
		return capabilities.Worktree{}, fmt.Errorf("branch %q is already checked out in %s", branch, owner)
	}
	returnBranch, err := chooseReturnBranch(ctx, adapter.Git, repo, branch, options.ReturnBranch)
	if err != nil {
		return capabilities.Worktree{}, err
	}
	if err := adapter.Git.Checkout(ctx, repo, returnBranch); err != nil {
		return capabilities.Worktree{}, err
	}

	createOptions := capabilities.WorktreeCreateOptions{
		Repo:        repo,
		Branch:      branch,
		Destination: destination,
		NoFocus:     options.NoFocus,
	}
	result, err := createAndOpenWorktree(ctx, createOptions, adapter.WorktreeAdapter)
	if err != nil {
		if restoreErr := adapter.Git.Checkout(ctx, repo, branch); restoreErr != nil {
			return capabilities.Worktree{}, errors.Join(err, fmt.Errorf("restore original branch %s: %w", branch, restoreErr))
		}
		return capabilities.Worktree{}, err
	}
	result.ReturnBranch = returnBranch
	return result, nil
}

func checkedOutElsewhere(ctx context.Context, git WorktreeGit, repo, branch string) (string, error) {
	owners, err := git.BranchOwners(ctx, repo, branch)
	if err != nil {
		return "", err
	}
	for _, owner := range owners {
		if !samePath(owner, repo) {
			return owner, nil
		}
	}
	return "", nil
}

// chooseReturnBranch prefers an explicit branch, then the previous named
// branch, then the repository default. An occupied candidate is skipped when
// it was suggested; an explicit one is refused.
func chooseReturnBranch(ctx context.Context, git WorktreeGit, repo, current, explicit string) (string, error) {
	if explicit != "" {
		if explicit == current {
			return "", fmt.Errorf("return branch %q is the branch being extracted", explicit)
		}
		exists, err := git.BranchExists(ctx, repo, explicit)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("return branch %q does not exist in %s", explicit, repo)
		}
		owner, err := checkedOutElsewhere(ctx, git, repo, explicit)
		if err != nil {
			return "", err
		}
		if owner != "" {
			return "", fmt.Errorf("return branch %q is already checked out in %s", explicit, owner)
		}
		return explicit, nil
	}

	previous, err := git.PreviousBranch(ctx, repo)
	if err != nil {
		return "", err
	}
	if previous != "" && previous != current {
		if branch, ok, err := eligibleReturnBranch(ctx, git, repo, previous); err != nil {
			return "", err
		} else if ok {
			return branch, nil
		}
	}
	def, err := git.DefaultBranch(ctx, repo)
	if err != nil {
		return "", err
	}
	if def != "" && def != current {
		if branch, ok, err := eligibleReturnBranch(ctx, git, repo, def); err != nil {
			return "", err
		} else if ok {
			return branch, nil
		}
	}
	return "", errors.New("no eligible return branch; pass --return-branch")
}

func eligibleReturnBranch(ctx context.Context, git WorktreeGit, repo, branch string) (string, bool, error) {
	exists, err := git.BranchExists(ctx, repo, branch)
	if err != nil || !exists {
		return "", false, err
	}
	owner, err := checkedOutElsewhere(ctx, git, repo, branch)
	if err != nil {
		return "", false, err
	}
	if owner != "" {
		return "", false, nil
	}
	return branch, true, nil
}

func samePath(a, b string) bool {
	return canonicalPath(a) == canonicalPath(b)
}

func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return filepath.Clean(path)
}

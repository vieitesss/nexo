package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

// Commands runs the Git queries and mutations that the worktree extraction
// transaction needs. It is the concrete adapter behind the lifecycle seam.
type Commands struct {
	Runner runner.CommandRunner
}

// New returns a Git command adapter over the shared command-runner seam.
func New(r runner.CommandRunner) *Commands {
	return &Commands{Runner: r}
}

func (c *Commands) run(ctx context.Context, repo string, args ...string) ([]byte, error) {
	if c == nil || c.Runner == nil {
		return nil, fmt.Errorf("git %s requires a command runner", strings.Join(args, " "))
	}
	return c.Runner.Run(ctx, "git", append([]string{"-C", repo}, args...)...)
}

// CurrentBranch returns the named branch checked out at repo, or an empty
// string when HEAD is detached.
func (c *Commands) CurrentBranch(ctx context.Context, repo string) (string, error) {
	output, err := c.run(ctx, repo, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("read current branch of %s: %w", repo, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// IsClean reports whether repo has no staged, unstaged, or untracked changes.
func (c *Commands) IsClean(ctx context.Context, repo string) (bool, error) {
	output, err := c.run(ctx, repo, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return false, fmt.Errorf("check %s: %w", repo, err)
	}
	return strings.TrimSpace(string(output)) == "", nil
}

// BranchOwners lists the worktree paths where branch is checked out.
func (c *Commands) BranchOwners(ctx context.Context, repo, branch string) ([]string, error) {
	output, err := c.run(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees of %s: %w", repo, err)
	}
	return worktreeOwners(output, branch), nil
}

// WorktreeRoots lists every registered worktree root, including the original
// checkout. It is used to reject destinations that would nest repository state
// inside an existing checkout.
func (c *Commands) WorktreeRoots(ctx context.Context, repo string) ([]string, error) {
	output, err := c.run(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees of %s: %w", repo, err)
	}
	return worktreeRoots(output), nil
}

func worktreeRoots(output []byte) []string {
	var roots []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if root := strings.TrimSpace(strings.TrimPrefix(line, "worktree ")); root != "" {
				roots = append(roots, root)
			}
		}
	}
	return roots
}

func worktreeOwners(output []byte, branch string) []string {
	var owners []string
	current := ""
	for _, line := range strings.Split(string(output), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch refs/heads/"):
			if strings.TrimPrefix(line, "branch refs/heads/") == branch {
				owners = append(owners, current)
			}
		}
	}
	return owners
}

// PreviousBranch returns the nearest distinct, existing local branch HEAD was
// on before the current one. Detached checkouts, redundant self-checkouts, and
// deleted historical branches are skipped.
func (c *Commands) PreviousBranch(ctx context.Context, repo string) (string, error) {
	current, err := c.CurrentBranch(ctx, repo)
	if err != nil {
		return "", nil
	}
	for index := 1; index <= 100; index++ {
		output, err := c.run(ctx, repo, "rev-parse", "--abbrev-ref", "@{-"+strconv.Itoa(index)+"}")
		if err != nil {
			break
		}
		name := strings.TrimSpace(string(output))
		if name == "" || name == "HEAD" || name == current {
			continue
		}
		exists, err := c.BranchExists(ctx, repo, name)
		if err != nil {
			return "", err
		}
		if exists {
			return name, nil
		}
	}
	return "", nil
}

// DefaultBranch returns the first existing repository default candidate: the
// remote HEAD, then init.defaultBranch, then a conventional main or master
// branch. Initialization preferences that do not exist in this repository are
// ignored.
func (c *Commands) DefaultBranch(ctx context.Context, repo string) (string, error) {
	var candidates []string
	if output, err := c.run(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(string(output)), "origin/"); name != "" {
			candidates = append(candidates, name)
		}
	}
	if output, err := c.run(ctx, repo, "config", "--get", "init.defaultBranch"); err == nil {
		if name := strings.TrimSpace(string(output)); name != "" {
			candidates = append(candidates, name)
		}
	}
	candidates = append(candidates, "main", "master")
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		exists, err := c.BranchExists(ctx, repo, candidate)
		if err != nil {
			return "", err
		}
		if exists {
			return candidate, nil
		}
	}
	return "", nil
}

// BranchExists reports whether a local branch ref exists.
func (c *Commands) BranchExists(ctx context.Context, repo, branch string) (bool, error) {
	if _, err := c.run(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return false, nil
	}
	return true, nil
}

// Checkout switches repo to branch.
func (c *Commands) Checkout(ctx context.Context, repo, branch string) error {
	if _, err := c.run(ctx, repo, "checkout", "--no-overwrite-ignore", branch); err != nil {
		return fmt.Errorf("checkout %s in %s: %w", branch, repo, err)
	}
	return nil
}

package git

import (
	"context"
	"fmt"
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
	output, err := c.run(ctx, repo, "status", "--porcelain")
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

// PreviousBranch returns the branch HEAD was on before the current one, or an
// empty string when there is no named previous branch.
func (c *Commands) PreviousBranch(ctx context.Context, repo string) (string, error) {
	output, err := c.run(ctx, repo, "rev-parse", "--abbrev-ref", "@{-1}")
	if err != nil {
		return "", nil
	}
	name := strings.TrimSpace(string(output))
	if name == "" || name == "HEAD" {
		return "", nil
	}
	return name, nil
}

// DefaultBranch returns the repository default branch: the remote HEAD, then
// init.defaultBranch, then a conventional main or master branch.
func (c *Commands) DefaultBranch(ctx context.Context, repo string) (string, error) {
	if output, err := c.run(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(string(output)), "origin/"); name != "" {
			return name, nil
		}
	}
	if output, err := c.run(ctx, repo, "config", "--get", "init.defaultBranch"); err == nil {
		if name := strings.TrimSpace(string(output)); name != "" {
			return name, nil
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if exists, err := c.BranchExists(ctx, repo, candidate); err != nil {
			return "", err
		} else if exists {
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
	if _, err := c.run(ctx, repo, "checkout", branch); err != nil {
		return fmt.Errorf("checkout %s in %s: %w", branch, repo, err)
	}
	return nil
}

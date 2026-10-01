package deletion

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

// GitInspector is the command-runner adapter for deletion preflight. A
// repository is allowed to have no remote; a configured remote that cannot be
// verified is recorded as a hard blocker.
type GitInspector struct{ Runner runner.CommandRunner }

func (g GitInspector) run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if g.Runner == nil {
		return nil, errors.New("git check requires a command runner")
	}
	return g.Runner.Run(ctx, "git", append([]string{"-C", path}, args...)...)
}

// Inspect returns Git facts. Non-Git directories are not errors; a failure of
// the initial repository identity check is returned so callers can make it a
// hard blocker rather than guessing.
func (g GitInspector) Inspect(ctx context.Context, path string) (GitState, error) {
	if g.Runner == nil {
		return GitState{}, errors.New("git check requires a command runner")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gitDirOutput, err := g.run(checkCtx, path, "rev-parse", "--git-dir")
	if err != nil {
		return GitState{}, nil
	}
	state := GitState{IsRepository: true}
	commonOutput, commonErr := g.run(ctx, path, "rev-parse", "--git-common-dir")
	topOutput, topErr := g.run(ctx, path, "rev-parse", "--show-toplevel")
	if commonErr != nil || topErr != nil {
		return state, fmt.Errorf("read repository identity: %w", errors.Join(commonErr, topErr))
	}
	gitDir := absoluteGitPath(path, strings.TrimSpace(string(gitDirOutput)))
	commonDir := absoluteGitPath(path, strings.TrimSpace(string(commonOutput)))
	top := absoluteGitPath(path, strings.TrimSpace(string(topOutput)))
	if commonDir != "" && top != "" && (filepath.Clean(gitDir) != filepath.Clean(commonDir) || filepath.Clean(filepath.Dir(commonDir)) != filepath.Clean(top)) {
		state.LinkedWorktree = true
		state.PrimaryPath = filepath.Clean(filepath.Dir(commonDir))
	}
	worktrees, wtErr := g.run(ctx, path, "worktree", "list", "--porcelain")
	if wtErr != nil {
		return state, fmt.Errorf("list worktrees: %w", wtErr)
	}
	roots := parseWorktreeRoots(worktrees)
	if !state.LinkedWorktree {
		for _, root := range roots {
			if filepath.Clean(root) != filepath.Clean(top) {
				state.DependentWorktrees = append(state.DependentWorktrees, root)
			}
		}
	}
	status, statusErr := g.run(ctx, path, "status", "--porcelain=v2", "--branch", "--untracked-files=all")
	if statusErr != nil {
		return state, fmt.Errorf("read repository status: %w", statusErr)
	}
	var hasUpstream bool
	state.Dirty, state.Untracked, state.Unpushed, hasUpstream = parseStatus(status)
	if !hasUpstream {
		// A named branch without an upstream has no independently verifiable
		// pushed boundary; treat existing commits as local-only data.
		if _, headErr := g.run(ctx, path, "rev-parse", "--verify", "--quiet", "HEAD"); headErr == nil {
			state.Unpushed = true
		}
	}
	if _, stashErr := g.run(ctx, path, "rev-parse", "--verify", "--quiet", "refs/stash"); stashErr == nil {
		state.Stashes = true
	}
	remotes, remoteErr := g.run(ctx, path, "remote")
	if remoteErr != nil {
		state.RemoteCheckError = remoteErr.Error()
		return state, nil
	}
	state.RemoteChecked = true
	for _, remote := range strings.Fields(string(remotes)) {
		if _, checkErr := g.run(ctx, path, "ls-remote", remote); checkErr != nil {
			state.RemoteCheckError = fmt.Sprintf("%s: %v", remote, checkErr)
			state.RemoteChecked = false
			break
		}
	}
	return state, nil
}

func absoluteGitPath(base, value string) string {
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	if absolute, err := filepath.Abs(value); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(value)
}

func parseWorktreeRoots(output []byte) []string {
	var roots []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if path := strings.TrimSpace(strings.TrimPrefix(line, "worktree ")); path != "" {
				roots = append(roots, filepath.Clean(path))
			}
		}
	}
	return roots
}

func parseStatus(output []byte) (dirty, untracked, unpushed, hasUpstream bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "# branch.upstream ") {
			hasUpstream = true
			continue
		}
		if strings.HasPrefix(line, "# branch.ab ") {
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) == 2 && strings.HasPrefix(fields[0], "+") {
				var count int
				if _, err := fmt.Sscanf(fields[0], "+%d", &count); err == nil && count > 0 {
					unpushed = true
				}
			}
			continue
		}
		if strings.HasPrefix(line, "? ") {
			untracked = true
			dirty = true
			continue
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			dirty = true
		}
	}
	return dirty, untracked, unpushed, hasUpstream
}

// RemoveWorktree deliberately omits --force. Git's refusal is a hard safety
// blocker, even when a caller requested force for other warnings.
func (g GitInspector) RemoveWorktree(ctx context.Context, repo, path string) error {
	if g.Runner == nil {
		return errors.New("worktree removal requires a command runner")
	}
	_, err := g.Runner.Run(ctx, "git", "-C", repo, "worktree", "remove", path)
	if err != nil {
		return fmt.Errorf("git worktree remove %q: %w", path, err)
	}
	return nil
}

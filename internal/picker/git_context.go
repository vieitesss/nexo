package picker

import (
	"bufio"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

// GitContext is the repository state displayed beside a project place. Counts
// are read from the checkout and its local refs; loading never contacts a
// remote.
type GitContext struct {
	Identity    string
	Adds        int
	Deletes     int
	Dirty       bool
	Ahead       int
	Behind      int
	CommonDir   string
	PrimaryPath string
}

// GitLoader loads one repository's context. Implementations must not block
// the picker model; the model calls them from Bubble Tea commands.
type GitLoader interface {
	Load(context.Context, string) (GitContext, error)
}

const (
	DefaultGitConcurrency = 4
	DefaultGitTimeout     = 500 * time.Millisecond
)

// GitContextLoader reads repository state using the command-runner seam.
type GitContextLoader struct {
	Runner         runner.CommandRunner
	MaxConcurrency int
	PerRepoTimeout time.Duration
}

// NewGitContextLoader constructs a loader with bounded, short-lived Git
// operations. A nil runner produces an error for each repository rather than
// falling back to direct process execution.
func NewGitContextLoader(commandRunner runner.CommandRunner) *GitContextLoader {
	return &GitContextLoader{
		Runner:         commandRunner,
		MaxConcurrency: DefaultGitConcurrency,
		PerRepoTimeout: DefaultGitTimeout,
	}
}

func (l *GitContextLoader) GitConcurrency() int {
	if l == nil || l.MaxConcurrency <= 0 {
		return DefaultGitConcurrency
	}
	return l.MaxConcurrency
}

func (l *GitContextLoader) GitTimeout() time.Duration {
	if l == nil || l.PerRepoTimeout <= 0 {
		return DefaultGitTimeout
	}
	return l.PerRepoTimeout
}

// Load reads branch identity, local divergence, changes and worktree
// ancestry. Every Git call is scoped to path and no fetch-capable command is
// used.
func (l *GitContextLoader) Load(parent context.Context, path string) (GitContext, error) {
	if l == nil || l.Runner == nil {
		return GitContext{}, errors.New("git context has no command runner")
	}
	ctx := parent
	if ctx == nil {
		ctx = context.Background()
	}
	cancel := func() {}
	if timeout := l.GitTimeout(); timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	return loadGitContext(ctx, l.Runner, path)
}

func loadGitContext(ctx context.Context, commandRunner runner.CommandRunner, path string) (GitContext, error) {
	statusOutput, err := commandRunner.Run(ctx, "git", "-C", path, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return GitContext{}, err
	}
	result := parseGitStatus(statusOutput)
	if !result.initial {
		diffOutput, diffErr := commandRunner.Run(ctx, "git", "-C", path, "diff", "--numstat", "HEAD", "--")
		if diffErr != nil {
			return GitContext{}, diffErr
		}
		adds, deletes := parseNumstat(diffOutput)
		result.Adds, result.Deletes = adds, deletes
	}

	commonOutput, err := commandRunner.Run(ctx, "git", "-C", path, "rev-parse", "--git-common-dir")
	if err != nil {
		return GitContext{}, err
	}
	topOutput, err := commandRunner.Run(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitContext{}, err
	}
	commonDir := absoluteGitPath(path, strings.TrimSpace(string(commonOutput)))
	topLevel := absoluteGitPath(path, strings.TrimSpace(string(topOutput)))
	result.CommonDir = commonDir
	if commonDir != "" && topLevel != "" {
		primary := filepath.Dir(commonDir)
		if !samePath(primary, topLevel) {
			result.PrimaryPath = primary
		}
	}
	return result.GitContext, nil
}

type parsedStatus struct {
	GitContext
	initial bool
}

func parseGitStatus(output []byte) parsedStatus {
	var result parsedStatus
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if head == "(detached)" {
				continue
			}
			if strings.HasPrefix(head, "(") {
				result.initial = head == "(initial)"
				continue
			}
			result.Identity = head
		case strings.HasPrefix(line, "# branch.oid "):
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if oid == "(initial)" || oid == "(unknown)" {
				result.initial = oid == "(initial)"
				continue
			}
			if result.Identity == "" {
				result.Identity = shortSHA(oid)
			}
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) == 2 {
				result.Ahead = parseSignedCount(fields[0], '+')
				result.Behind = parseSignedCount(fields[1], '-')
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			result.Dirty = true
		}
	}
	return result
}

func parseSignedCount(value string, sign byte) int {
	if len(value) < 2 || value[0] != sign {
		return 0
	}
	count, err := strconv.Atoi(value[1:])
	if err != nil || count < 0 {
		return 0
	}
	return count
}

func parseNumstat(output []byte) (int, int) {
	var adds, deletes int
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "\t", 3)
		if len(fields) < 2 {
			continue
		}
		if value, err := strconv.Atoi(fields[0]); err == nil {
			adds += value
		}
		if value, err := strconv.Atoi(fields[1]); err == nil {
			deletes += value
		}
	}
	return adds, deletes
}

func shortSHA(value string) string {
	if len(value) > 7 {
		return value[:7]
	}
	return value
}

func absoluteGitPath(base, value string) string {
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(value)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(absolute)
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

// GitContextMsg is emitted by an asynchronous repository load.
type GitContextMsg struct {
	Path    string
	Context GitContext
	Err     error
}

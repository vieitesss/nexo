package picker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

// PullRequestState is the GitHub review state shown for a checkout's branch.
type PullRequestState string

const (
	PullRequestOpen   PullRequestState = "open"
	PullRequestMerged PullRequestState = "merged"
	PullRequestClosed PullRequestState = "closed"
)

// PullRequest is the pull request badge for one checkout. The zero value means
// "no pull request to show".
type PullRequest struct {
	Number int
	State  PullRequestState
}

// PullRequestLoader resolves the pull request shown for one checkout. A zero
// PullRequest with a nil error means the checkout has no matching request.
// Implementations must not block the picker model; the model calls them from
// Bubble Tea commands, and every failure is a silent absence to callers.
type PullRequestLoader interface {
	Load(context.Context, string) (PullRequest, error)
}

const (
	DefaultPullRequestConcurrency = 2
	DefaultPullRequestTimeout     = 2 * time.Second
)

// GitHubPullRequestLoader asks gh for the pull request of a checkout's
// upstream branch. It runs only when gh is on PATH and the checkout's remote
// is GitHub, and it caches the answer by checkout and upstream identity so a
// refresh does not re-query. Local Git metadata is derived with short-lived
// commands; gh itself is never run against another forge.
type GitHubPullRequestLoader struct {
	Runner         runner.CommandRunner
	LookPath       func(string) (string, error)
	MaxConcurrency int
	PerRepoTimeout time.Duration

	mu    sync.Mutex
	cache map[string]PullRequest
}

// NewGitHubPullRequestLoader constructs a loader with bounded, short-lived
// operations. A nil runner produces an error instead of falling back to
// direct process execution; a nil LookPath uses exec.LookPath.
func NewGitHubPullRequestLoader(commandRunner runner.CommandRunner) *GitHubPullRequestLoader {
	return &GitHubPullRequestLoader{Runner: commandRunner, cache: make(map[string]PullRequest)}
}

func (l *GitHubPullRequestLoader) PullRequestConcurrency() int {
	if l == nil || l.MaxConcurrency <= 0 {
		return DefaultPullRequestConcurrency
	}
	return l.MaxConcurrency
}

func (l *GitHubPullRequestLoader) PullRequestTimeout() time.Duration {
	if l == nil || l.PerRepoTimeout <= 0 {
		return DefaultPullRequestTimeout
	}
	return l.PerRepoTimeout
}

// Load resolves the pull request for path. It never guesses: a detached
// branch, a non-GitHub remote, a fork-owned head, a missing gh, an
// unauthenticated gh, or a failed command all resolve to the zero value.
func (l *GitHubPullRequestLoader) Load(parent context.Context, path string) (PullRequest, error) {
	if l == nil || l.Runner == nil {
		return PullRequest{}, errors.New("pull request has no command runner")
	}
	if !l.ghAvailable() {
		return PullRequest{}, nil
	}
	ctx := parent
	if ctx == nil {
		ctx = context.Background()
	}
	cancel := func() {}
	if timeout := l.PullRequestTimeout(); timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	upstream, ok := loadCheckoutUpstream(ctx, l.Runner, path)
	if !ok {
		// A deadline or cancellation is not a silent absence: the command was
		// interrupted, not answered. Everything else is.
		if err := ctx.Err(); err != nil {
			return PullRequest{}, err
		}
		return PullRequest{}, nil
	}
	key := upstream.key(path)
	if value, cached := l.cached(key); cached {
		return value, nil
	}
	value, err := loadGitHubPullRequest(ctx, l.Runner, upstream)
	if err != nil {
		return PullRequest{}, err
	}
	l.store(key, value)
	return value, nil
}

func (l *GitHubPullRequestLoader) ghAvailable() bool {
	lookPath := l.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath("gh")
	return err == nil
}

func (l *GitHubPullRequestLoader) cached(key string) (PullRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	value, ok := l.cache[key]
	return value, ok
}

func (l *GitHubPullRequestLoader) store(key string, value PullRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cache == nil {
		l.cache = make(map[string]PullRequest)
	}
	l.cache[key] = value
}

// checkoutUpstream is the identity the cache and the gh query are keyed on:
// the GitHub repository a checkout pushes to and the branch its upstream
// points at.
type checkoutUpstream struct {
	owner  string
	repo   string
	branch string
}

func (u checkoutUpstream) key(path string) string {
	return path + "\x00" + u.owner + "\x00" + u.repo + "\x00" + u.branch
}

func loadCheckoutUpstream(ctx context.Context, commandRunner runner.CommandRunner, path string) (checkoutUpstream, bool) {
	branchOutput, err := commandRunner.Run(ctx, "git", "-C", path, "branch", "--show-current")
	if err != nil {
		return checkoutUpstream{}, false
	}
	branch := strings.TrimSpace(string(branchOutput))
	if branch == "" {
		return checkoutUpstream{}, false
	}
	remote := "origin"
	// for-each-ref reports the configured upstream remote and branch exactly,
	// which avoids guessing where a remote name ends and a branch name
	// begins in "origin/feature/something".
	upstreamOutput, err := commandRunner.Run(ctx, "git", "-C", path,
		"for-each-ref",
		"--format=%(refname)%09%(upstream:remotename)%09%(upstream:remoteref)",
		"--", "refs/heads/"+branch)
	if err == nil {
		if name, ref, ok := parseUpstreamRef(string(upstreamOutput), branch); ok {
			remote, branch = name, ref
		}
	}
	remoteOutput, err := commandRunner.Run(ctx, "git", "-C", path, "remote", "get-url", remote)
	if err != nil {
		return checkoutUpstream{}, false
	}
	owner, repo, ok := parseGitHubRemote(strings.TrimSpace(string(remoteOutput)))
	if !ok {
		return checkoutUpstream{}, false
	}
	return checkoutUpstream{owner: owner, repo: repo, branch: branch}, true
}

// parseUpstreamRef extracts the remote name and head branch from one
// for-each-ref line. It only accepts the exact local branch ref, so a branch
// name that happens to glob more than one ref cannot misidentify the
// upstream.
func parseUpstreamRef(output, branch string) (remote, ref string, ok bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] != "refs/heads/"+branch {
			continue
		}
		remote, ref = fields[1], fields[2]
		if remote == "" || !strings.HasPrefix(ref, "refs/heads/") {
			return "", "", false
		}
		return remote, strings.TrimPrefix(ref, "refs/heads/"), true
	}
	return "", "", false
}

// parseGitHubRemote extracts the owner and repository from a github.com
// remote URL, in scp-like, ssh, git, or https form. Any other host, and any
// URL without an owner and repository, is not a GitHub upstream.
func parseGitHubRemote(raw string) (owner, repo string, ok bool) {
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		host, path = parsed.Hostname(), parsed.Path
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"):
		rest := raw[strings.IndexByte(raw, '@')+1:]
		colon := strings.IndexByte(rest, ':')
		host, path = rest[:colon], rest[colon+1:]
	default:
		return "", "", false
	}
	if !strings.EqualFold(host, "github.com") {
		return "", "", false
	}
	path = strings.Trim(strings.TrimSuffix(strings.TrimSpace(path), ".git"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

type gitHubPullRequest struct {
	Number              int    `json:"number"`
	State               string `json:"state"`
	HeadRefName         string `json:"headRefName"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

func loadGitHubPullRequest(ctx context.Context, commandRunner runner.CommandRunner, upstream checkoutUpstream) (PullRequest, error) {
	output, err := commandRunner.Run(ctx, "gh", "pr", "list",
		"--repo", upstream.owner+"/"+upstream.repo,
		"--head", upstream.branch,
		"--state", "all",
		"--json", "number,state,headRefName,headRepositoryOwner")
	if err != nil {
		return PullRequest{}, err
	}
	var candidates []gitHubPullRequest
	if err := json.Unmarshal(output, &candidates); err != nil {
		return PullRequest{}, fmt.Errorf("decode gh pull request list: %w", err)
	}
	for _, candidate := range candidates {
		// gh filters by branch name but can return same-named branches from
		// forks. Only the upstream owner's branch is this checkout's request.
		if candidate.HeadRefName != upstream.branch {
			continue
		}
		if !strings.EqualFold(candidate.HeadRepositoryOwner.Login, upstream.owner) {
			continue
		}
		state, ok := pullRequestState(candidate.State)
		if !ok || candidate.Number <= 0 {
			continue
		}
		return PullRequest{Number: candidate.Number, State: state}, nil
	}
	return PullRequest{}, nil
}

func pullRequestState(value string) (PullRequestState, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "OPEN":
		return PullRequestOpen, true
	case "MERGED":
		return PullRequestMerged, true
	case "CLOSED":
		return PullRequestClosed, true
	default:
		return "", false
	}
}

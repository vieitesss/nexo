package picker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

const pullRequestRepo = "/repo"

// pullRequestScript is the mutable state one fake repository answers with.
// Tests adjust it between loads to prove that the cache is keyed by upstream
// identity rather than by checkout alone.
type pullRequestScript struct {
	mu             sync.Mutex
	branch         string
	upstreamRemote string
	upstreamRef    string
	remoteURL      string
	ghResponse     []byte
	ghErr          error
	ghCalls        int
}

func (s *pullRequestScript) setBranch(branch, upstreamRemote, upstreamRef string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.branch, s.upstreamRemote, s.upstreamRef = branch, upstreamRemote, upstreamRef
}

func (s *pullRequestScript) setRemoteURL(remoteURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remoteURL = remoteURL
}

func (s *pullRequestScript) answer(call string) ([]byte, error) {
	s.mu.Lock()
	branch, upstreamRemote, upstreamRef, remoteURL := s.branch, s.upstreamRemote, s.upstreamRef, s.remoteURL
	response, ghErr := s.ghResponse, s.ghErr
	s.mu.Unlock()
	switch {
	case call == "git -C "+pullRequestRepo+" branch --show-current":
		return []byte(branch + "\n"), nil
	case strings.HasPrefix(call, "git -C "+pullRequestRepo+" for-each-ref "):
		return []byte("refs/heads/" + branch + "\t" + upstreamRemote + "\t" + upstreamRef + "\n"), nil
	case strings.HasPrefix(call, "git -C "+pullRequestRepo+" remote get-url "):
		return []byte(remoteURL + "\n"), nil
	case strings.HasPrefix(call, "gh pr list "):
		s.mu.Lock()
		s.ghCalls++
		s.mu.Unlock()
		return response, ghErr
	}
	return nil, errors.New("unexpected command: " + call)
}

type pullRequestRunner struct {
	mu     sync.Mutex
	calls  []string
	script *pullRequestScript
	run    func(call string, ctx context.Context) ([]byte, error)
}

func (r *pullRequestRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(call, ctx)
	}
	return r.script.answer(call)
}

func (r *pullRequestRunner) ghCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, call := range r.calls {
		if strings.HasPrefix(call, "gh ") {
			out = append(out, call)
		}
	}
	return out
}

func (r *pullRequestRunner) allCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func newTestPullRequestLoader(commandRunner runner.CommandRunner) *GitHubPullRequestLoader {
	loader := NewGitHubPullRequestLoader(commandRunner)
	loader.LookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	return loader
}

func githubRepoScript() *pullRequestScript {
	return &pullRequestScript{
		branch:         "feature",
		upstreamRemote: "origin",
		upstreamRef:    "refs/heads/feature",
		remoteURL:      "git@github.com:acme/widgets.git",
	}
}

func TestPullRequestLoaderMatchesUpstreamOwnerAndState(t *testing.T) {
	for _, test := range []struct {
		name  string
		state string
		want  PullRequestState
	}{
		{"open", "OPEN", PullRequestOpen},
		{"merged", "MERGED", PullRequestMerged},
		{"closed", "CLOSED", PullRequestClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := githubRepoScript()
			script.ghResponse = []byte(`[{"number":128,"state":"` + test.state + `","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
			commandRunner := &pullRequestRunner{script: script}
			value, err := newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
			if err != nil {
				t.Fatal(err)
			}
			if value.Number != 128 || value.State != test.want {
				t.Fatalf("pull request = %#v, want #128 %s", value, test.want)
			}
			calls := commandRunner.ghCalls()
			want := "gh pr list --repo acme/widgets --head feature --state all --json number,state,headRefName,headRepositoryOwner"
			if len(calls) != 1 || calls[0] != want {
				t.Fatalf("gh calls = %#v, want %q", calls, want)
			}
		})
	}
}

func TestPullRequestLoaderIgnoresForkOwnedAndMismatchedHeads(t *testing.T) {
	script := githubRepoScript()
	script.ghResponse = []byte(`[
		{"number":1,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"forker"}},
		{"number":2,"state":"OPEN","headRefName":"other","headRepositoryOwner":{"login":"acme"}},
		{"number":3,"state":"NOT_A_STATE","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}
	]`)
	value, err := newTestPullRequestLoader(&pullRequestRunner{script: script}).Load(context.Background(), pullRequestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if value.Number != 0 || value.State != "" {
		t.Fatalf("fork or mismatched pull requests were shown: %#v", value)
	}

	// A matching request after a fork request is still accepted.
	script.ghResponse = []byte(`[
		{"number":1,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"forker"}},
		{"number":2,"state":"CLOSED","headRefName":"feature","headRepositoryOwner":{"login":"ACME"}}
	]`)
	value, err = newTestPullRequestLoader(&pullRequestRunner{script: script}).Load(context.Background(), pullRequestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if value.Number != 2 || value.State != PullRequestClosed {
		t.Fatalf("owner match must be case-insensitive: %#v", value)
	}
}

func TestPullRequestLoaderReturnsNothingWithoutACandidate(t *testing.T) {
	for _, response := range []string{"[]", "null"} {
		script := githubRepoScript()
		script.ghResponse = []byte(response)
		value, err := newTestPullRequestLoader(&pullRequestRunner{script: script}).Load(context.Background(), pullRequestRepo)
		if err != nil || value.Number != 0 {
			t.Fatalf("empty response %q = %#v, %v", response, value, err)
		}
	}
}

func TestPullRequestLoaderSkipsNonGitHubAndDetachedCheckouts(t *testing.T) {
	nonGitHub := githubRepoScript()
	nonGitHub.remoteURL = "git@gitlab.com:acme/widgets.git"
	nonGitHub.ghResponse = []byte(`[{"number":1,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
	commandRunner := &pullRequestRunner{script: nonGitHub}
	value, err := newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
	if err != nil || value.Number != 0 {
		t.Fatalf("non-GitHub remote = %#v, %v", value, err)
	}
	if len(commandRunner.ghCalls()) != 0 {
		t.Fatalf("gh ran for a non-GitHub remote: %#v", commandRunner.ghCalls())
	}

	detached := githubRepoScript()
	detached.branch = ""
	detached.ghResponse = []byte(`[{"number":1,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
	commandRunner = &pullRequestRunner{script: detached}
	value, err = newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
	if err != nil || value.Number != 0 {
		t.Fatalf("detached checkout = %#v, %v", value, err)
	}
	if len(commandRunner.ghCalls()) != 0 {
		t.Fatalf("gh ran for a detached checkout: %#v", commandRunner.ghCalls())
	}

	local := githubRepoScript()
	local.remoteURL = "/srv/git/widgets.git"
	commandRunner = &pullRequestRunner{script: local}
	if value, err = newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo); err != nil || value.Number != 0 {
		t.Fatalf("local remote = %#v, %v", value, err)
	}
	if len(commandRunner.ghCalls()) != 0 {
		t.Fatalf("gh ran for a local remote: %#v", commandRunner.ghCalls())
	}
}

func TestPullRequestLoaderFallsBackToOriginWithoutUpstream(t *testing.T) {
	script := githubRepoScript()
	script.upstreamRemote = ""
	script.upstreamRef = ""
	script.ghResponse = []byte(`[{"number":7,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
	commandRunner := &pullRequestRunner{script: script}
	value, err := newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
	if err != nil || value.Number != 7 {
		t.Fatalf("upstream fallback = %#v, %v", value, err)
	}
	if got := commandRunner.ghCalls(); len(got) != 1 || !strings.Contains(got[0], "--head feature") {
		t.Fatalf("gh calls = %#v", got)
	}
}

func TestPullRequestLoaderSkipsMissingGhAndFailedCommandsSilently(t *testing.T) {
	missingRunner := &pullRequestRunner{script: githubRepoScript()}
	missing := NewGitHubPullRequestLoader(missingRunner)
	missing.LookPath = func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }
	value, err := missing.Load(context.Background(), pullRequestRepo)
	if err != nil || value.Number != 0 {
		t.Fatalf("missing gh = %#v, %v", value, err)
	}
	if len(missingRunner.allCalls()) != 0 {
		t.Fatal("commands ran without gh on PATH")
	}

	failing := githubRepoScript()
	failing.ghResponse = nil
	failing.ghErr = errors.New("gh: authentication required")
	commandRunner := &pullRequestRunner{script: failing}
	value, err = newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
	if value.Number != 0 {
		t.Fatalf("failed gh returned a pull request: %#v", value)
	}
	if err == nil {
		t.Fatal("failed gh returned no error for the silent caller to ignore")
	}
	if len(commandRunner.ghCalls()) != 1 {
		t.Fatalf("gh calls = %#v", commandRunner.ghCalls())
	}

	malformed := githubRepoScript()
	malformed.ghResponse = []byte("not json")
	value, err = newTestPullRequestLoader(&pullRequestRunner{script: malformed}).Load(context.Background(), pullRequestRepo)
	if value.Number != 0 || err == nil {
		t.Fatalf("malformed gh output = %#v, %v", value, err)
	}
}

func TestPullRequestLoaderCachesByUpstreamIdentity(t *testing.T) {
	script := githubRepoScript()
	script.ghResponse = []byte(`[{"number":11,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
	commandRunner := &pullRequestRunner{script: script}
	loader := newTestPullRequestLoader(commandRunner)

	first, err := loader.Load(context.Background(), pullRequestRepo)
	if err != nil || first.Number != 11 {
		t.Fatalf("first load = %#v, %v", first, err)
	}
	second, err := loader.Load(context.Background(), pullRequestRepo)
	if err != nil || second.Number != 11 {
		t.Fatalf("cached load = %#v, %v", second, err)
	}
	if got := len(commandRunner.ghCalls()); got != 1 {
		t.Fatalf("gh calls after a cache hit = %d, want 1", got)
	}

	// A different upstream identity is a different cache key.
	script.setBranch("other", "origin", "refs/heads/other")
	script.ghResponse = []byte(`[{"number":22,"state":"MERGED","headRefName":"other","headRepositoryOwner":{"login":"acme"}}]`)
	third, err := loader.Load(context.Background(), pullRequestRepo)
	if err != nil || third.Number != 22 {
		t.Fatalf("new branch load = %#v, %v", third, err)
	}
	if got := len(commandRunner.ghCalls()); got != 2 {
		t.Fatalf("gh calls after a branch change = %d, want 2", got)
	}

	// The same branch on a different remote is a different identity too.
	script.setRemoteURL("git@github.com:other/widgets.git")
	script.ghResponse = []byte(`[{"number":33,"state":"CLOSED","headRefName":"other","headRepositoryOwner":{"login":"other"}}]`)
	fourth, err := loader.Load(context.Background(), pullRequestRepo)
	if err != nil || fourth.Number != 33 {
		t.Fatalf("new remote load = %#v, %v", fourth, err)
	}
	if got := len(commandRunner.ghCalls()); got != 3 {
		t.Fatalf("gh calls after a remote change = %d, want 3", got)
	}
}

func TestPullRequestLoaderCachesAbsenceWithoutRerunningGh(t *testing.T) {
	script := githubRepoScript()
	script.ghResponse = []byte(`[]`)
	commandRunner := &pullRequestRunner{script: script}
	loader := newTestPullRequestLoader(commandRunner)
	for i := 0; i < 3; i++ {
		value, err := loader.Load(context.Background(), pullRequestRepo)
		if err != nil || value.Number != 0 {
			t.Fatalf("load %d = %#v, %v", i, value, err)
		}
	}
	if got := len(commandRunner.ghCalls()); got != 1 {
		t.Fatalf("gh calls for an absent pull request = %d, want 1", got)
	}
}

type blockingPullRequestRunner struct {
	started chan struct{}
}

func (r blockingPullRequestRunner) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPullRequestLoaderBoundsEachCallWithATimeout(t *testing.T) {
	loader := newTestPullRequestLoader(blockingPullRequestRunner{started: make(chan struct{}, 1)})
	loader.PerRepoTimeout = 10 * time.Millisecond
	if _, err := loader.Load(context.Background(), pullRequestRepo); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow command error = %v, want deadline exceeded", err)
	}
}

func TestPullRequestLoaderReportsConfiguredBounds(t *testing.T) {
	if got := NewGitHubPullRequestLoader(nil).PullRequestConcurrency(); got != DefaultPullRequestConcurrency {
		t.Fatalf("default concurrency = %d, want %d", got, DefaultPullRequestConcurrency)
	}
	configured := NewGitHubPullRequestLoader(nil)
	configured.MaxConcurrency = 3
	if got := configured.PullRequestConcurrency(); got != 3 {
		t.Fatalf("configured concurrency = %d, want 3", got)
	}
	if got := configured.PullRequestTimeout(); got != DefaultPullRequestTimeout {
		t.Fatalf("default timeout = %v, want %v", got, DefaultPullRequestTimeout)
	}
	configured.PerRepoTimeout = time.Second
	if got := configured.PullRequestTimeout(); got != time.Second {
		t.Fatalf("configured timeout = %v, want 1s", got)
	}
}

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
	local.remoteURL = "/srv/git:archive@backup/widgets.git"
	commandRunner = &pullRequestRunner{script: local}
	if value, err = newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo); err != nil || value.Number != 0 {
		t.Fatalf("local remote = %#v, %v", value, err)
	}
	if len(commandRunner.ghCalls()) != 0 {
		t.Fatalf("gh ran for a local remote: %#v", commandRunner.ghCalls())
	}
}

func TestPullRequestLoaderRefusesGuessedUpstreams(t *testing.T) {
	// An upstream-less branch whose only candidate is somebody else's
	// same-named branch on origin must show nothing.
	missing := githubRepoScript()
	missing.upstreamRemote = ""
	missing.upstreamRef = ""
	missing.ghResponse = []byte(`[{"number":7,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
	commandRunner := &pullRequestRunner{script: missing}
	value, err := newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
	if err != nil || value.Number != 0 {
		t.Fatalf("missing upstream = %#v, %v", value, err)
	}
	if got := commandRunner.ghCalls(); len(got) != 0 {
		t.Fatalf("gh ran for a missing upstream: %#v", got)
	}

	// A failed upstream lookup is the same silent absence, not a fallback.
	failing := &pullRequestRunner{script: githubRepoScript(), run: func(call string, _ context.Context) ([]byte, error) {
		if strings.HasPrefix(call, "git -C "+pullRequestRepo+" for-each-ref ") {
			return nil, errors.New("fatal: bad object refs/heads/feature")
		}
		return nil, errors.New("unexpected command: " + call)
	}}
	if value, err = newTestPullRequestLoader(failing).Load(context.Background(), pullRequestRepo); err != nil || value.Number != 0 {
		t.Fatalf("failed upstream lookup = %#v, %v", value, err)
	}
	if got := failing.ghCalls(); len(got) != 0 {
		t.Fatalf("gh ran after a failed upstream lookup: %#v", got)
	}
}

func TestPullRequestLoaderQueriesTheUpstreamBranchName(t *testing.T) {
	script := githubRepoScript()
	script.setBranch("local-name", "origin", "refs/heads/remote-name")
	script.ghResponse = []byte(`[
		{"number":1,"state":"OPEN","headRefName":"local-name","headRepositoryOwner":{"login":"acme"}},
		{"number":2,"state":"OPEN","headRefName":"remote-name","headRepositoryOwner":{"login":"acme"}}
	]`)
	commandRunner := &pullRequestRunner{script: script}
	value, err := newTestPullRequestLoader(commandRunner).Load(context.Background(), pullRequestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if value.Number != 2 || value.State != PullRequestOpen {
		t.Fatalf("upstream branch request = %#v, want #2 open", value)
	}
	calls := commandRunner.ghCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--head remote-name") {
		t.Fatalf("gh calls = %#v, want the upstream branch name", calls)
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

func TestPullRequestLoaderRevalidatesAfterFreshness(t *testing.T) {
	for _, test := range []struct {
		name       string
		before     string
		after      string
		wantBefore PullRequest
		wantAfter  PullRequest
	}{
		{
			"state change",
			`[{"number":11,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`,
			`[{"number":11,"state":"MERGED","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`,
			PullRequest{Number: 11, State: PullRequestOpen},
			PullRequest{Number: 11, State: PullRequestMerged},
		},
		{
			"absence to present",
			`[]`,
			`[{"number":22,"state":"CLOSED","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`,
			PullRequest{},
			PullRequest{Number: 22, State: PullRequestClosed},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := githubRepoScript()
			script.ghResponse = []byte(test.before)
			commandRunner := &pullRequestRunner{script: script}
			now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
			loader := newTestPullRequestLoader(commandRunner)
			loader.Now = func() time.Time { return now }

			value, err := loader.Load(context.Background(), pullRequestRepo)
			if err != nil || value != test.wantBefore {
				t.Fatalf("first load = %#v, %v, want %#v", value, err, test.wantBefore)
			}
			now = now.Add(loader.PullRequestFreshness())
			script.ghResponse = []byte(test.after)
			if value, err = loader.Load(context.Background(), pullRequestRepo); err != nil || value != test.wantAfter {
				t.Fatalf("revalidated load = %#v, %v, want %#v", value, err, test.wantAfter)
			}
			if got := len(commandRunner.ghCalls()); got != 2 {
				t.Fatalf("gh calls = %d, want 2 after freshness elapsed", got)
			}
		})
	}
}

func TestPullRequestLoaderReusesAFreshAnswer(t *testing.T) {
	script := githubRepoScript()
	script.ghResponse = []byte(`[{"number":11,"state":"OPEN","headRefName":"feature","headRepositoryOwner":{"login":"acme"}}]`)
	commandRunner := &pullRequestRunner{script: script}
	now := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	loader := newTestPullRequestLoader(commandRunner)
	loader.Now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		value, err := loader.Load(context.Background(), pullRequestRepo)
		if err != nil || value.Number != 11 {
			t.Fatalf("load %d = %#v, %v", i, value, err)
		}
		now = now.Add(loader.PullRequestFreshness() / 4)
	}
	if got := len(commandRunner.ghCalls()); got != 1 {
		t.Fatalf("gh calls inside the freshness window = %d, want 1", got)
	}
}

func TestParseGitHubRemoteAcceptsAndRejects(t *testing.T) {
	for _, raw := range []string{
		"/srv/git:archive@backup/widgets.git",
		"/srv/git/widgets.git",
		"git@:widgets.git",
		"@github.com:acme/widgets.git",
		"git@github.com:",
		"git@github.com",
		"/srv/git@github.com:acme/widgets.git",
		"github.com:acme/widgets.git",
		"git://gitlab.com/acme/widgets.git",
	} {
		if owner, repo, ok := parseGitHubRemote(raw); ok {
			t.Fatalf("remote %q parsed as %s/%s", raw, owner, repo)
		}
	}
	for _, test := range []struct{ raw, owner, repo string }{
		{"git@github.com:acme/widgets.git", "acme", "widgets"},
		{"ssh://git@github.com/acme/widgets.git", "acme", "widgets"},
		{"https://github.com/acme/widgets", "acme", "widgets"},
		{" git@github.com:acme/widgets.git\n", "acme", "widgets"},
	} {
		owner, repo, ok := parseGitHubRemote(test.raw)
		if !ok || owner != test.owner || repo != test.repo {
			t.Fatalf("remote %q = %s/%s, %t, want %s/%s", test.raw, owner, repo, ok, test.owner, test.repo)
		}
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
	if got := configured.PullRequestFreshness(); got != DefaultPullRequestFreshness {
		t.Fatalf("default freshness = %v, want %v", got, DefaultPullRequestFreshness)
	}
	configured.Freshness = time.Minute
	if got := configured.PullRequestFreshness(); got != time.Minute {
		t.Fatalf("configured freshness = %v, want 1m", got)
	}
}

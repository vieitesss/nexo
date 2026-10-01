package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

var _ capabilities.WorktreeExtractor = (*Backend)(nil)

// realGitRunner dispatches git through the real executable while keeping the
// Herdr command surface faked.
type realGitRunner struct {
	inner *fakeRunner
}

func (r *realGitRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "git" {
		r.inner.calls = append(r.inner.calls, call{name: name, args: append([]string(nil), args...)})
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	return r.inner.Run(ctx, name, args...)
}

func runHerdrTestGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func herdrTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runHerdrTestGit(t, repo, "init", "-q")
	runHerdrTestGit(t, repo, "config", "user.email", "test@example.com")
	runHerdrTestGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runHerdrTestGit(t, repo, "add", "README")
	runHerdrTestGit(t, repo, "commit", "-qm", "initial")
	return repo
}

func TestExtractWorktreeUsesNativeHerdrCreateForExistingBranch(t *testing.T) {
	repo := herdrTestRepo(t)
	base := runHerdrTestGit(t, repo, "branch", "--show-current")
	runHerdrTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")

	runner := &realGitRunner{inner: &fakeRunner{}}
	got, err := New(runner).ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, NoFocus: true,
	})
	if err != nil {
		t.Fatalf("ExtractWorktree() error = %v", err)
	}
	if got.Branch != "feature" || got.ReturnBranch != base {
		t.Fatalf("result = %#v, want feature extracted with return %q", got, base)
	}
	if branch := runHerdrTestGit(t, repo, "branch", "--show-current"); branch != base {
		t.Fatalf("original branch = %q, want %q", branch, base)
	}
	want := "herdr worktree create --cwd " + repo + " --branch feature --path " + destination
	var found bool
	for _, command := range runner.inner.calls {
		if command.name == "herdr" && strings.Join(command.args, " ") == strings.TrimPrefix(want, "herdr ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("calls = %#v, want %q", runner.inner.calls, want)
	}
}

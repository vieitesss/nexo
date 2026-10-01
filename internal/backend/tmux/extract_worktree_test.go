package tmux

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

func TestExtractWorktreeSwitchesOriginalAndCreatesRealWorktree(t *testing.T) {
	repo := testGitRepo(t)
	base := strings.TrimSpace(runTestGitOutput(t, repo, "branch", "--show-current"))
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")

	runner := &realGitTmuxRunner{}
	got, err := New(runner, true).ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, NoFocus: true,
	})
	if err != nil {
		t.Fatalf("ExtractWorktree() error = %v", err)
	}
	if got.Branch != "feature" || got.ReturnBranch != base {
		t.Fatalf("result = %#v, want feature extracted with return %q", got, base)
	}
	if _, err := os.Stat(filepath.Join(destination, "README")); err != nil {
		t.Fatalf("worktree files: %v", err)
	}
	if branch := strings.TrimSpace(runTestGitOutput(t, repo, "branch", "--show-current")); branch != base {
		t.Fatalf("original branch = %q, want %q", branch, base)
	}
}

func runTestGitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

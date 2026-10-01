package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type realGitTmuxRunner struct {
	failSwitch bool
	calls      []call
}

func (r *realGitTmuxRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
	if name == "git" {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	operation := args[0]
	if operation == "-u" && len(args) > 1 {
		operation = args[1]
	}
	switch operation {
	case "has-session":
		return []byte("can't find session"), errors.New("can't find session")
	case "switch-client":
		if r.failSwitch {
			return []byte("switch failed"), errors.New("switch failed")
		}
	case "new-session", "kill-session":
		return nil, nil
	}
	return nil, nil
}

func (r *realGitTmuxRunner) RunInteractive(context.Context, string, ...string) error {
	return nil
}

func testGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runTestGit(t, repo, "init", "-q")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", "README")
	runTestGit(t, repo, "commit", "-qm", "initial")
	return repo
}

func runTestGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}

func TestCreateWorktreeWithNewBranchUsesRealGit(t *testing.T) {
	repo := testGitRepo(t)
	destination := filepath.Join(t.TempDir(), "new")
	runner := &realGitTmuxRunner{}
	if _, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "feature", Destination: destination, NewBranch: true,
	}); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README")); err != nil {
		t.Fatalf("worktree files: %v", err)
	}
	runTestGit(t, repo, "show-ref", "--verify", "refs/heads/feature")
}

func TestCreateWorktreeWithExistingBranchUsesRealGit(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "branch", "existing")
	destination := filepath.Join(t.TempDir(), "existing")
	if _, err := New(&realGitTmuxRunner{}, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "existing", Destination: destination,
	}); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README")); err != nil {
		t.Fatalf("worktree files: %v", err)
	}
}

func TestCreateWorktreeRollbackRemovesRealGitStateAndSession(t *testing.T) {
	repo := testGitRepo(t)
	destination := filepath.Join(t.TempDir(), "rollback")
	runner := &realGitTmuxRunner{failSwitch: true}
	_, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "rollback", Destination: destination, NewBranch: true,
	})
	if err == nil || !strings.Contains(err.Error(), "switch failed") {
		t.Fatalf("CreateWorktree() error = %v, want switch failure", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination stat error = %v, want absent", statErr)
	}
	if output, gitErr := exec.Command("git", "-C", repo, "show-ref", "--verify", "refs/heads/rollback").CombinedOutput(); gitErr == nil {
		t.Fatalf("rollback branch still exists: %s", output)
	}
	foundKill := false
	for _, command := range runner.calls {
		if command.name == "tmux" && len(command.args) > 1 && command.args[1] == "kill-session" {
			foundKill = true
		}
	}
	if !foundKill {
		t.Fatalf("calls = %#v, want kill-session rollback", runner.calls)
	}
}

func TestCreateWorktreeExistingDestinationDoesNotTouchGit(t *testing.T) {
	repo := testGitRepo(t)
	destination := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &realGitTmuxRunner{}
	_, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "feature", Destination: destination, NewBranch: true,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want none", runner.calls)
	}
}

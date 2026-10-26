package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
)

// realGitTmuxRunner runs Git for real and fakes the tmux commands, so the
// adapter is proven against repositories instead of a canned Git script.
type realGitTmuxRunner struct {
	calls []call
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

func TestCreateWorktreeWithRealGitOpensTheBackgroundSession(t *testing.T) {
	repo := testGitRepo(t)
	destination := filepath.Join(t.TempDir(), "new")
	runner := &realGitTmuxRunner{}
	got, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "feature", Destination: destination, NewBranch: true,
	})
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if !got.Container.Created || got.Container.Name != projects.TargetName(destination) {
		t.Fatalf("container = %#v, want the session created for the worktree", got.Container)
	}
	want := call{name: "tmux", args: []string{"-u", "new-session", "-d", "-s", projects.TargetName(destination), "-c", destination}}
	found := false
	for _, command := range nativeCalls(runner.calls) {
		if reflect.DeepEqual(command, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("calls = %#v, want %#v", nativeCalls(runner.calls), want)
	}
}

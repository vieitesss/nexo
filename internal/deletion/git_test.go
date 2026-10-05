package deletion

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

func TestGitInspectorUsesRegisteredPrimaryForSeparateMetadataWorktree(t *testing.T) {
	setGitTestEnv(t)
	root := realTempDir(t)
	metadata := filepath.Join(root, "metadata.git")
	primary := filepath.Join(root, "primary")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, primary, "init", "--separate-git-dir", metadata, "-q")
	gitTestCommand(t, primary, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, primary, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(primary, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, primary, "add", "tracked")
	gitTestCommand(t, primary, "commit", "-qm", "initial")
	linked := filepath.Join(root, "linked")
	gitTestCommand(t, primary, "worktree", "add", "-q", "-b", "linked", linked)

	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LinkedWorktree || state.PrimaryPath != metadata {
		t.Fatalf("state = %#v, want registered primary %q", state, metadata)
	}
}

func TestGitInspectorRejectsMalformedStashAndPreflightBlocksBothModes(t *testing.T) {
	setGitTestEnv(t)
	repo := gitTestRepo(t)
	gitTestStash(t, repo)
	if err := os.WriteFile(filepath.Join(repo, ".git", "refs", "stash"), []byte("not-a-ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, inspectErr := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if inspectErr == nil {
		t.Fatal("Inspect() error = nil for malformed stash ref")
	}
	observation, err := InspectFilesystem(OSFileSystem{}, repo)
	if err != nil {
		t.Fatal(err)
	}
	observation.Candidate, observation.Configured = true, true
	observation.GitError = inspectErr
	plan := Preflight(observation)
	if plan.CanProceed || !hasCode(plan.Blockers, "git-check-failed") {
		t.Fatalf("plan = %#v, want hard stash inspection blocker", plan)
	}
	for _, permanent := range []bool{false, true} {
		if err := Execute(context.Background(), plan, true, permanent, OSFileSystem{}, nil, nil); err == nil {
			t.Fatalf("Execute(permanent=%v) error = nil for blocked stash inspection", permanent)
		}
		if _, statErr := os.Lstat(repo); statErr != nil {
			t.Fatalf("repository changed after blocked permanent=%v deletion: %v", permanent, statErr)
		}
	}
}

func TestGitInspectorStashCommandFailureIsHardError(t *testing.T) {
	setGitTestEnv(t)
	repo := gitTestRepo(t)
	failure := errors.New("stash inspection deadline")
	_, err := (GitInspector{Runner: stashCommandRunner{base: runner.ExecRunner{}, err: failure}}).Inspect(context.Background(), repo)
	if !errors.Is(err, failure) {
		t.Fatalf("Inspect() error = %v, want stash command failure", err)
	}
}

func TestGitInspectorAcceptsAbsentAndPackedStashRefs(t *testing.T) {
	setGitTestEnv(t)
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "packed"}[packed], func(t *testing.T) {
			repo := gitTestRepo(t)
			if !packed {
				state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
				if err != nil || state.Stashes {
					t.Fatalf("Inspect() = %#v, %v; want no stash", state, err)
				}
				return
			}
			gitTestStash(t, repo)
			gitTestCommand(t, repo, "pack-refs", "--all", "--prune")
			state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
			if err != nil || !state.Stashes {
				t.Fatalf("Inspect() = %#v, %v; want packed stash", state, err)
			}
		})
	}
}

type stashCommandRunner struct {
	base runner.CommandRunner
	err  error
}

func (r stashCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "refs/stash") {
		return nil, r.err
	}
	return r.base.Run(ctx, name, args...)
}

func gitTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTestCommand(t, repo, "init", "-q")
	gitTestCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "tracked")
	gitTestCommand(t, repo, "commit", "-qm", "initial")
	return repo
}

func gitTestStash(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "stash", "push", "-q", "--", "tracked")
}

func gitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func setGitTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

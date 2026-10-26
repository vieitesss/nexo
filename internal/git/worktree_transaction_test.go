package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/runner"
)

// answer is one queued reply to a scripted Git command.
type answer struct {
	output string
	err    error
}

// contextState is the state of a command context when the command started.
type contextState struct {
	err         error
	hasDeadline bool
}

// scriptedGit records the Git commands the transaction runs and answers each
// one from a queue, so command order, compensation and branch ownership are
// asserted without a repository. A command with no queued answer is a failure
// of the test script, not of the transaction under test.
type scriptedGit struct {
	answers map[string][]answer
	calls   []string
	started []contextState
	onCall  func(context.Context, string)
}

func (g *scriptedGit) reply(key string, replies ...answer) {
	if g.answers == nil {
		g.answers = make(map[string][]answer)
	}
	g.answers[key] = append(g.answers[key], replies...)
}

func (g *scriptedGit) ok(key string) {
	g.reply(key, answer{})
}

func (g *scriptedGit) fail(key string, err error) {
	g.reply(key, answer{err: err})
}

func (g *scriptedGit) run(ctx context.Context, args ...string) ([]byte, error) {
	key := "git " + strings.Join(args, " ")
	g.calls = append(g.calls, key)
	_, hasDeadline := ctx.Deadline()
	g.started = append(g.started, contextState{err: ctx.Err(), hasDeadline: hasDeadline})
	if g.onCall != nil {
		g.onCall(ctx, key)
	}
	replies := g.answers[key]
	if len(replies) == 0 {
		return nil, errors.New("unexpected git command: " + key)
	}
	g.answers[key] = replies[1:]
	return []byte(replies[0].output), replies[0].err
}

func showRefCommand(branch string) string {
	return "git -C /repo show-ref --verify --quiet refs/heads/" + branch
}

func worktreeAddCommand(args ...string) string {
	return "git -C /repo worktree add " + strings.Join(args, " ")
}

func worktreeRemoveCommand(path string) string {
	return "git -C /repo worktree remove --force " + path
}

func branchDeleteCommand(branch string) string {
	return "git -C /repo branch -D " + branch
}

func createOptions(destination string, newBranch bool) capabilities.WorktreeCreateOptions {
	return capabilities.WorktreeCreateOptions{
		Repo:        "/repo",
		Branch:      "feature",
		Destination: destination,
		NewBranch:   newBranch,
	}
}

func TestCreateRefusesAnExistingBranchBeforeAdding(t *testing.T) {
	commands := &scriptedGit{}
	commands.ok(showRefCommand("feature"))

	got, err := NewWorktrees(commands.run).Create(context.Background(), createOptions("/work/feature", true))
	if err == nil || err.Error() != `branch "feature" already exists` {
		t.Fatalf("Create() error = %v, want the existing-branch rejection", err)
	}
	if got != (capabilities.Worktree{}) {
		t.Fatalf("Create() = %#v, want no worktree", got)
	}
	if want := []string{showRefCommand("feature")}; !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want %#v", commands.calls, want)
	}
}

func TestCreateAddsANewBranchOrAnExistingOne(t *testing.T) {
	t.Run("new branch", func(t *testing.T) {
		commands := &scriptedGit{}
		commands.fail(showRefCommand("feature"), errors.New("missing branch"))
		commands.ok(worktreeAddCommand("-b", "feature", "/work/feature"))
		got, err := NewWorktrees(commands.run).Create(context.Background(), createOptions("/work/feature", true))
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		want := []string{showRefCommand("feature"), worktreeAddCommand("-b", "feature", "/work/feature")}
		if !reflect.DeepEqual(commands.calls, want) {
			t.Fatalf("calls = %#v, want %#v", commands.calls, want)
		}
		if wantResult := (capabilities.Worktree{Path: "/work/feature", Branch: "feature", Repo: "/repo", NewBranch: true}); got != wantResult {
			t.Fatalf("Create() = %#v, want %#v", got, wantResult)
		}
	})

	t.Run("existing branch", func(t *testing.T) {
		commands := &scriptedGit{}
		commands.ok(worktreeAddCommand("/work/feature", "feature"))
		got, err := NewWorktrees(commands.run).Create(context.Background(), createOptions("/work/feature", false))
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		want := []string{worktreeAddCommand("/work/feature", "feature")}
		if !reflect.DeepEqual(commands.calls, want) {
			t.Fatalf("calls = %#v, want %#v", commands.calls, want)
		}
		if wantResult := (capabilities.Worktree{Path: "/work/feature", Branch: "feature", Repo: "/repo"}); got != wantResult {
			t.Fatalf("Create() = %#v, want %#v", got, wantResult)
		}
	})
}

func TestCreateCompensatesAFailedAdd(t *testing.T) {
	destination := t.TempDir()
	commands := &scriptedGit{}
	commands.fail(showRefCommand("feature"), errors.New("missing branch"))
	commands.fail(worktreeAddCommand("-b", "feature", destination), errors.New("add failed"))
	commands.ok(worktreeRemoveCommand(destination))
	commands.ok(showRefCommand("feature"))
	commands.ok(branchDeleteCommand("feature"))

	_, err := NewWorktrees(commands.run).Create(context.Background(), createOptions(destination, true))
	if err == nil || !strings.Contains(err.Error(), "add failed") {
		t.Fatalf("Create() error = %v, want the add failure", err)
	}
	want := []string{
		showRefCommand("feature"),
		worktreeAddCommand("-b", "feature", destination),
		worktreeRemoveCommand(destination),
		showRefCommand("feature"),
		branchDeleteCommand("feature"),
	}
	if !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want %#v", commands.calls, want)
	}
}

func TestCreateCompensationNeverDeletesABranchItDoesNotOwn(t *testing.T) {
	t.Run("existing branch", func(t *testing.T) {
		destination := t.TempDir()
		commands := &scriptedGit{}
		commands.fail(worktreeAddCommand(destination, "feature"), errors.New("add failed"))
		commands.ok(worktreeRemoveCommand(destination))

		if _, err := NewWorktrees(commands.run).Create(context.Background(), createOptions(destination, false)); err == nil {
			t.Fatal("Create() error = nil, want the add failure")
		}
		want := []string{worktreeAddCommand(destination, "feature"), worktreeRemoveCommand(destination)}
		if !reflect.DeepEqual(commands.calls, want) {
			t.Fatalf("calls = %#v, want no branch deletion", commands.calls)
		}
	})

	t.Run("branch already gone", func(t *testing.T) {
		destination := t.TempDir()
		commands := &scriptedGit{}
		commands.fail(showRefCommand("feature"), errors.New("missing branch"))
		commands.fail(worktreeAddCommand("-b", "feature", destination), errors.New("add failed"))
		commands.ok(worktreeRemoveCommand(destination))
		commands.fail(showRefCommand("feature"), errors.New("missing branch"))

		if _, err := NewWorktrees(commands.run).Create(context.Background(), createOptions(destination, true)); err == nil {
			t.Fatal("Create() error = nil, want the add failure")
		}
		want := []string{
			showRefCommand("feature"),
			worktreeAddCommand("-b", "feature", destination),
			worktreeRemoveCommand(destination),
			showRefCommand("feature"),
		}
		if !reflect.DeepEqual(commands.calls, want) {
			t.Fatalf("calls = %#v, want no branch deletion", commands.calls)
		}
	})
}

func TestCreateCompensationSkipsAnAbsentDestination(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	commands := &scriptedGit{}
	commands.fail(showRefCommand("feature"), errors.New("missing branch"))
	commands.fail(worktreeAddCommand("-b", "feature", destination), errors.New("add failed"))
	commands.fail(showRefCommand("feature"), errors.New("missing branch"))

	if _, err := NewWorktrees(commands.run).Create(context.Background(), createOptions(destination, true)); err == nil {
		t.Fatal("Create() error = nil, want the add failure")
	}
	want := []string{showRefCommand("feature"), worktreeAddCommand("-b", "feature", destination), showRefCommand("feature")}
	if !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want no worktree removal", commands.calls)
	}
}

func TestCreateCompensationUsesALiveBoundedCleanupContext(t *testing.T) {
	destination := t.TempDir()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	commands := &scriptedGit{}
	commands.fail(showRefCommand("feature"), errors.New("missing branch"))
	commands.fail(worktreeAddCommand("-b", "feature", destination), errors.New("add failed"))
	commands.ok(worktreeRemoveCommand(destination))
	commands.ok(showRefCommand("feature"))
	commands.ok(branchDeleteCommand("feature"))
	commands.onCall = func(_ context.Context, key string) {
		if key == worktreeAddCommand("-b", "feature", destination) {
			cancel()
		}
	}

	if _, err := NewWorktrees(commands.run).Create(parent, createOptions(destination, true)); err == nil {
		t.Fatal("Create() error = nil, want the add failure")
	}
	addIndex := 1
	if commands.calls[addIndex] != worktreeAddCommand("-b", "feature", destination) {
		t.Fatalf("calls = %#v, want the add at index %d", commands.calls, addIndex)
	}
	if len(commands.calls) <= addIndex+1 {
		t.Fatalf("calls = %#v, want compensation commands", commands.calls)
	}
	for index := addIndex + 1; index < len(commands.calls); index++ {
		state := commands.started[index]
		if state.err != nil {
			t.Fatalf("cleanup command %q context error = %v, want a context detached from the caller", commands.calls[index], state.err)
		}
		if !state.hasDeadline {
			t.Fatalf("cleanup command %q has no deadline, want the bounded cleanup deadline", commands.calls[index])
		}
	}
}

func TestRemoveDeletesTheWorktreeAndOnlyItsOwnBranch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feature")
	t.Run("created branch", func(t *testing.T) {
		commands := &scriptedGit{}
		commands.ok(worktreeRemoveCommand(path))
		commands.ok(branchDeleteCommand("feature"))
		worktree := capabilities.Worktree{Repo: "/repo", Path: path, Branch: "feature", NewBranch: true}
		if err := NewWorktrees(commands.run).Remove(context.Background(), worktree); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		want := []string{worktreeRemoveCommand(path), branchDeleteCommand("feature")}
		if !reflect.DeepEqual(commands.calls, want) {
			t.Fatalf("calls = %#v, want %#v", commands.calls, want)
		}
	})

	t.Run("branch kept", func(t *testing.T) {
		commands := &scriptedGit{}
		commands.ok(worktreeRemoveCommand(path))
		worktree := capabilities.Worktree{Repo: "/repo", Path: path, Branch: "feature"}
		if err := NewWorktrees(commands.run).Remove(context.Background(), worktree); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if want := []string{worktreeRemoveCommand(path)}; !reflect.DeepEqual(commands.calls, want) {
			t.Fatalf("calls = %#v, want no branch deletion", commands.calls)
		}
	})
}

func TestRemoveResolvesTheDestinationBeforeRemovingIt(t *testing.T) {
	base := t.TempDir()
	commands := &scriptedGit{}
	commands.ok(worktreeRemoveCommand(filepath.Join(base, "feature")))

	worktree := capabilities.Worktree{Repo: "/repo", Path: base + "/nested/../feature", Branch: "feature"}
	if err := NewWorktrees(commands.run).Remove(context.Background(), worktree); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if want := []string{worktreeRemoveCommand(filepath.Join(base, "feature"))}; !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want the resolved destination", commands.calls)
	}
}

func TestRemoveJoinsEveryCleanupFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feature")
	commands := &scriptedGit{}
	commands.fail(worktreeRemoveCommand(path), errors.New("remove failed"))
	commands.fail(branchDeleteCommand("feature"), errors.New("branch failed"))

	worktree := capabilities.Worktree{Repo: "/repo", Path: path, Branch: "feature", NewBranch: true}
	err := NewWorktrees(commands.run).Remove(context.Background(), worktree)
	if err == nil || !strings.Contains(err.Error(), "remove failed") || !strings.Contains(err.Error(), "branch failed") {
		t.Fatalf("Remove() error = %v, want both cleanup failures", err)
	}
}

func TestRollbackSkipsWhatIsAlreadyAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feature")
	commands := &scriptedGit{}
	commands.fail(showRefCommand("feature"), errors.New("missing branch"))

	worktree := capabilities.Worktree{Repo: "/repo", Path: path, Branch: "feature", NewBranch: true}
	if err := NewWorktrees(commands.run).Rollback(context.Background(), worktree); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if want := []string{showRefCommand("feature")}; !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want no removal for absent resources", commands.calls)
	}
}

func TestRollbackRemovesTheWorktreeAndItsBranch(t *testing.T) {
	path := t.TempDir()
	commands := &scriptedGit{}
	commands.ok(worktreeRemoveCommand(path))
	commands.ok(showRefCommand("feature"))
	commands.ok(branchDeleteCommand("feature"))

	worktree := capabilities.Worktree{Repo: "/repo", Path: path, Branch: "feature", NewBranch: true}
	if err := NewWorktrees(commands.run).Rollback(context.Background(), worktree); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	want := []string{worktreeRemoveCommand(path), showRefCommand("feature"), branchDeleteCommand("feature")}
	if !reflect.DeepEqual(commands.calls, want) {
		t.Fatalf("calls = %#v, want %#v", commands.calls, want)
	}
}

// execGit runs the real Git binary through the same command seam the backends
// inject, so the transaction is proven against a repository and not only a
// script.
func execGit(ctx context.Context, args ...string) ([]byte, error) {
	return runner.ExecRunner{}.Run(ctx, "git", args...)
}

func TestCreateAddsARealWorktreeWithANewBranch(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	destination := filepath.Join(t.TempDir(), "feature")

	got, err := NewWorktrees(execGit).Create(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "feature", Destination: destination, NewBranch: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.Path != destination || got.Branch != "feature" || !got.NewBranch || got.Repo != repo {
		t.Fatalf("Create() = %#v, want the created worktree", got)
	}
	if _, err := os.Stat(filepath.Join(destination, "README")); err != nil {
		t.Fatalf("worktree checkout missing: %v", err)
	}
	gitWorktreeTestRun(t, repo, "show-ref", "--verify", "refs/heads/feature")
}

func TestCreateChecksOutAnExistingRealBranch(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	base := gitWorktreeTestRun(t, repo, "branch", "--show-current")
	gitWorktreeTestRun(t, repo, "branch", "release")
	destination := filepath.Join(t.TempDir(), "release")

	if _, err := NewWorktrees(execGit).Create(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "release", Destination: destination,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README")); err != nil {
		t.Fatalf("worktree checkout missing: %v", err)
	}
	if branch := gitWorktreeTestRun(t, repo, "branch", "--show-current"); branch != base {
		t.Fatalf("original checkout is on %q, want %q", branch, base)
	}
}

func TestCreateRefusesARealExistingBranch(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	gitWorktreeTestRun(t, repo, "branch", "feature")
	destination := filepath.Join(t.TempDir(), "feature")

	_, err := NewWorktrees(execGit).Create(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "feature", Destination: destination, NewBranch: true,
	})
	if err == nil || !strings.Contains(err.Error(), `branch "feature" already exists`) {
		t.Fatalf("Create() error = %v, want the existing-branch rejection", err)
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after the rejection: %v", statErr)
	}
}

func TestCreateCompensationKeepsARealBranchItDidNotCreate(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	base := gitWorktreeTestRun(t, repo, "branch", "--show-current")
	gitWorktreeTestRun(t, repo, "branch", "occupied")
	destination := occupiedDestination(t)

	_, err := NewWorktrees(execGit).Create(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "occupied", Destination: destination,
	})
	if err == nil {
		t.Fatal("Create() error = nil, want the occupied-destination failure")
	}
	assertDirectoryIntact(t, destination)
	gitWorktreeTestRun(t, repo, "show-ref", "--verify", "refs/heads/occupied")
	if branch := gitWorktreeTestRun(t, repo, "branch", "--show-current"); branch != base {
		t.Fatalf("original checkout is on %q, want %q", branch, base)
	}
}

func TestCreateCompensationDeletesARealBranchItCreated(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	destination := occupiedDestination(t)

	_, err := NewWorktrees(execGit).Create(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: "feature", Destination: destination, NewBranch: true,
	})
	if err == nil {
		t.Fatal("Create() error = nil, want the occupied-destination failure")
	}
	assertDirectoryIntact(t, destination)
	if output, gitErr := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "show-ref", "--verify", "refs/heads/feature"); gitErr == nil {
		t.Fatalf("compensation kept the branch it created: %s", output)
	}
}

func TestRemoveHandlesARealWorktreeAndItsBranch(t *testing.T) {
	t.Run("created branch", func(t *testing.T) {
		repo := gitWorktreeTestRepo(t)
		worktree := createRealWorktree(t, repo, "feature")
		if err := NewWorktrees(execGit).Remove(context.Background(), worktree); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if _, err := os.Stat(worktree.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("worktree still exists after Remove(): %v", err)
		}
		if output, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "show-ref", "--verify", "refs/heads/feature"); err == nil {
			t.Fatalf("branch survived Remove(): %s", output)
		}
	})

	t.Run("branch kept", func(t *testing.T) {
		repo := gitWorktreeTestRepo(t)
		gitWorktreeTestRun(t, repo, "branch", "release")
		worktree := createRealWorktree(t, repo, "release")
		if err := NewWorktrees(execGit).Remove(context.Background(), worktree); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if _, err := os.Stat(worktree.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("worktree still exists after Remove(): %v", err)
		}
		gitWorktreeTestRun(t, repo, "show-ref", "--verify", "refs/heads/release")
	})
}

func TestRollbackRemovesARealWorktreeAndItsBranch(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	worktree := createRealWorktree(t, repo, "feature")
	if err := NewWorktrees(execGit).Rollback(context.Background(), worktree); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if _, err := os.Stat(worktree.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still exists after Rollback(): %v", err)
	}
	if output, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "show-ref", "--verify", "refs/heads/feature"); err == nil {
		t.Fatalf("branch survived Rollback(): %s", output)
	}
}

func createRealWorktree(t *testing.T, repo, branch string) capabilities.Worktree {
	t.Helper()
	newBranch := false
	if _, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		newBranch = true
	}
	worktree, err := NewWorktrees(execGit).Create(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: repo, Branch: branch, Destination: filepath.Join(t.TempDir(), branch), NewBranch: newBranch,
	})
	if err != nil {
		t.Fatalf("create worktree %q: %v", branch, err)
	}
	return worktree
}

// occupiedDestination returns a non-empty directory Git cannot create a
// worktree in, which is how the add failure path is reached with real Git.
func occupiedDestination(t *testing.T) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "keep.txt"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return destination
}

func assertDirectoryIntact(t *testing.T, directory string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(directory, "keep.txt")); err != nil {
		t.Fatalf("user directory was disturbed by compensation: %v", err)
	}
}

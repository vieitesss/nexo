package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

type fakeRunner struct {
	calls   []string
	outputs map[string][]byte
	errors  map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	if output, ok := f.outputs[key]; ok {
		return output, nil
	}
	return nil, errors.New("unexpected command " + key)
}

func TestCurrentBranch(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"git -C /repo branch --show-current": []byte("feature\n")}}
	got, err := New(runner).CurrentBranch(context.Background(), "/repo")
	if err != nil || got != "feature" {
		t.Fatalf("CurrentBranch() = %q, %v", got, err)
	}
}

func TestIsClean(t *testing.T) {
	clean := &fakeRunner{outputs: map[string][]byte{"git -C /repo status --porcelain --untracked-files=all --ignore-submodules=none": []byte("")}}
	if ok, err := New(clean).IsClean(context.Background(), "/repo"); err != nil || !ok {
		t.Fatalf("IsClean() = %t, %v", ok, err)
	}
	dirty := &fakeRunner{outputs: map[string][]byte{"git -C /repo status --porcelain --untracked-files=all --ignore-submodules=none": []byte(" M README\n")}}
	if ok, err := New(dirty).IsClean(context.Background(), "/repo"); err != nil || ok {
		t.Fatalf("IsClean() = %t, %v, want dirty", ok, err)
	}
}

func TestBranchOwnersParsesPorcelain(t *testing.T) {
	list := "worktree /repo\nHEAD aaaa\nbranch refs/heads/feature\n\n" +
		"worktree /elsewhere\nHEAD bbbb\nbranch refs/heads/occupied\n\n" +
		"worktree /detached\nHEAD cccc\ndetached\n"
	runner := &fakeRunner{outputs: map[string][]byte{"git -C /repo worktree list --porcelain": []byte(list)}}
	commands := New(runner)
	got, err := commands.BranchOwners(context.Background(), "/repo", "feature")
	if err != nil {
		t.Fatalf("BranchOwners() error = %v", err)
	}
	if want := []string{"/repo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BranchOwners(feature) = %v, want %v", got, want)
	}
	got, err = commands.BranchOwners(context.Background(), "/repo", "occupied")
	if err != nil {
		t.Fatalf("BranchOwners() error = %v", err)
	}
	if want := []string{"/elsewhere"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BranchOwners(occupied) = %v, want %v", got, want)
	}
	if got, _ := commands.BranchOwners(context.Background(), "/repo", "main"); len(got) != 0 {
		t.Fatalf("BranchOwners(main) = %v, want none", got)
	}
}

func TestPreviousBranchPropagatesCurrentBranchFailure(t *testing.T) {
	runner := &fakeRunner{
		errors: map[string]error{
			"git -C /repo branch --show-current": errors.New("boom"),
		},
	}
	if got, err := New(runner).PreviousBranch(context.Background(), "/repo"); err == nil || got != "" {
		t.Fatalf("PreviousBranch() on failure = %q, %v, want error", got, err)
	}
}

func TestPreviousBranchTreatsDetachedHistoryAsEmpty(t *testing.T) {
	detached := &fakeRunner{outputs: map[string][]byte{
		"git -C /repo branch --show-current":        []byte("feature\n"),
		"git -C /repo rev-parse --abbrev-ref @{-1}": []byte("HEAD\n"),
	}}
	if got, err := New(detached).PreviousBranch(context.Background(), "/repo"); err != nil || got != "" {
		t.Fatalf("PreviousBranch() on detached = %q, %v, want empty", got, err)
	}
}

func TestDefaultBranchFallsBackInOrder(t *testing.T) {
	withRemote := &fakeRunner{outputs: map[string][]byte{
		"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": []byte("origin/trunk\n"),
		"git -C /repo show-ref --verify --quiet refs/heads/trunk":    []byte(""),
	}}
	if got, err := New(withRemote).DefaultBranch(context.Background(), "/repo"); err != nil || got != "trunk" {
		t.Fatalf("DefaultBranch() = %q, %v, want trunk", got, err)
	}

	configured := &fakeRunner{
		errors: map[string]error{"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": errors.New("no remote")},
		outputs: map[string][]byte{
			"git -C /repo config --get init.defaultBranch":              []byte("develop\n"),
			"git -C /repo show-ref --verify --quiet refs/heads/develop": []byte(""),
		},
	}
	if got, err := New(configured).DefaultBranch(context.Background(), "/repo"); err != nil || got != "develop" {
		t.Fatalf("DefaultBranch() = %q, %v, want develop", got, err)
	}

	conventional := &fakeRunner{
		errors: map[string]error{
			"git -C /repo symbolic-ref --short refs/remotes/origin/HEAD": errors.New("no remote"),
			"git -C /repo config --get init.defaultBranch":               errors.New("unset"),
			"git -C /repo show-ref --verify --quiet refs/heads/main":     errors.New("missing"),
		},
		outputs: map[string][]byte{"git -C /repo show-ref --verify --quiet refs/heads/master": []byte("")},
	}
	if got, err := New(conventional).DefaultBranch(context.Background(), "/repo"); err != nil || got != "master" {
		t.Fatalf("DefaultBranch() = %q, %v, want master", got, err)
	}
}

func gitWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitWorktreeTestRun(t, repo, "init", "-q")
	gitWorktreeTestRun(t, repo, "config", "user.email", "test@example.com")
	gitWorktreeTestRun(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitWorktreeTestRun(t, repo, "add", "README")
	gitWorktreeTestRun(t, repo, "commit", "-qm", "initial")
	return repo
}

func gitWorktreeTestRun(t *testing.T, repo string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestPreviousBranchWalksPastDetachedAndRedundantCheckouts(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	base := gitWorktreeTestRun(t, repo, "branch", "--show-current")
	gitWorktreeTestRun(t, repo, "branch", "release")
	gitWorktreeTestRun(t, repo, "checkout", "-q", "release")
	gitWorktreeTestRun(t, repo, "checkout", "-q", "--detach")
	gitWorktreeTestRun(t, repo, "checkout", "-q", base)
	gitWorktreeTestRun(t, repo, "checkout", "-q", base)
	got, err := New(runner.ExecRunner{}).PreviousBranch(context.Background(), repo)
	if err != nil || got != "release" {
		t.Fatalf("PreviousBranch() = %q, %v, want release", got, err)
	}
}

func TestDefaultBranchSkipsMissingInitializationPreference(t *testing.T) {
	repo := gitWorktreeTestRepo(t)
	gitWorktreeTestRun(t, repo, "config", "init.defaultBranch", "trunk")
	got, err := New(runner.ExecRunner{}).DefaultBranch(context.Background(), repo)
	if err != nil || got != gitWorktreeTestRun(t, repo, "branch", "--show-current") {
		t.Fatalf("DefaultBranch() = %q, %v, want existing branch", got, err)
	}
}

func TestBranchExistsAndCheckout(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"git -C /repo show-ref --verify --quiet refs/heads/main": []byte(""),
		"git -C /repo checkout --no-overwrite-ignore main":       []byte(""),
	}}
	commands := New(runner)
	if ok, err := commands.BranchExists(context.Background(), "/repo", "main"); err != nil || !ok {
		t.Fatalf("BranchExists(main) = %t, %v", ok, err)
	}
	if ok, err := commands.BranchExists(context.Background(), "/repo", "missing"); err != nil || ok {
		t.Fatalf("BranchExists(missing) = %t, %v", ok, err)
	}
	if err := commands.Checkout(context.Background(), "/repo", "main"); err != nil {
		t.Fatalf("Checkout() error = %v", err)
	}
	if runner.calls[len(runner.calls)-1] != "git -C /repo checkout --no-overwrite-ignore main" {
		t.Fatalf("last call = %q", runner.calls[len(runner.calls)-1])
	}
}

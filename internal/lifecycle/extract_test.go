package lifecycle

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/runner"
)

func runTestGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
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

func currentBranch(t *testing.T, repo string) string {
	t.Helper()
	return runTestGit(t, repo, "branch", "--show-current")
}

func extractAdapter(gitAdapter *git.Commands, calls *extractCalls) ExtractAdapter {
	return ExtractAdapter{
		WorktreeAdapter: WorktreeAdapter{
			Create: func(_ context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
				calls.create = append(calls.create, options)
				if calls.createErr != nil {
					return capabilities.Worktree{}, calls.createErr
				}
				return capabilities.Worktree{}, nil
			},
			Open: func(_ context.Context, path string, options capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
				calls.open = append(calls.open, options)
				if calls.openErr != nil {
					return capabilities.OpenedContainer{}, calls.openErr
				}
				return capabilities.OpenedContainer{ID: "$1", Created: true}, nil
			},
			Rollback: func(_ context.Context, worktree capabilities.Worktree) error {
				calls.rolledBack = append(calls.rolledBack, worktree)
				return calls.rollbackErr
			},
		},
		Git: gitAdapter,
	}
}

type extractCalls struct {
	create      []capabilities.WorktreeCreateOptions
	createErr   error
	open        []capabilities.OpenOptions
	openErr     error
	rolledBack  []capabilities.Worktree
	rollbackErr error
}

func TestExtractWorktreeMovesBranchToDestination(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")

	var calls extractCalls
	got, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, NoFocus: true,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err != nil {
		t.Fatalf("ExtractWorktree() error = %v", err)
	}
	if len(calls.create) != 1 {
		t.Fatalf("create calls = %#v", calls.create)
	}
	if options := calls.create[0]; options.Repo != repo || options.Branch != "feature" || options.Destination != destination || options.NewBranch || !options.NoFocus {
		t.Fatalf("create options = %#v", options)
	}
	if len(calls.open) != 1 || !calls.open[0].NoFocus {
		t.Fatalf("open calls = %#v, want no-focus open", calls.open)
	}
	if got.Branch != "feature" || got.ReturnBranch != base || got.Path != destination {
		t.Fatalf("result = %#v", got)
	}
	if branch := currentBranch(t, repo); branch != base {
		t.Fatalf("original branch = %q, want %q", branch, base)
	}
}

func TestExtractWorktreeUsesExplicitReturnBranch(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	runTestGit(t, repo, "branch", "release")
	destination := filepath.Join(t.TempDir(), "feature")

	var calls extractCalls
	got, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: "release",
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err != nil {
		t.Fatalf("ExtractWorktree() error = %v", err)
	}
	if got.ReturnBranch != "release" {
		t.Fatalf("return branch = %q, want release", got.ReturnBranch)
	}
	if branch := currentBranch(t, repo); branch != "release" {
		t.Fatalf("original branch = %q, want release", branch)
	}
}

func TestExtractWorktreeFallsBackToDefaultBranchWithoutPrevious(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "branch", "feature")
	runTestGit(t, repo, "symbolic-ref", "HEAD", "refs/heads/feature")
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("setup branch = %q, want feature", branch)
	}
	destination := filepath.Join(t.TempDir(), "feature")

	var calls extractCalls
	got, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err != nil {
		t.Fatalf("ExtractWorktree() error = %v", err)
	}
	if got.ReturnBranch != base {
		t.Fatalf("return branch = %q, want default %q", got.ReturnBranch, base)
	}
}

func TestExtractWorktreeRefusesDirtyOriginal(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(repo, "dirty"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "feature")

	var calls extractCalls
	_, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("ExtractWorktree() error = %v, want dirty refusal", err)
	}
	if len(calls.create) != 0 {
		t.Fatalf("create calls = %#v, want none", calls.create)
	}
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("original branch = %q, want feature", branch)
	}
}

func TestExtractWorktreeRefusesExistingDestination(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}

	var calls extractCalls
	_, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("ExtractWorktree() error = %v, want existing destination refusal", err)
	}
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("original branch = %q, want feature", branch)
	}
}

func TestExtractWorktreeRefusesReturnBranchCheckedOutElsewhere(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	other := filepath.Join(t.TempDir(), "occupied")
	runTestGit(t, repo, "worktree", "add", "-q", other, "-b", "occupied")
	destination := filepath.Join(t.TempDir(), "feature")

	var calls extractCalls
	_, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: "occupied",
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err == nil || !strings.Contains(err.Error(), "already checked out") {
		t.Fatalf("ExtractWorktree() error = %v, want ownership refusal", err)
	}
	if len(calls.create) != 0 {
		t.Fatalf("create calls = %#v, want none", calls.create)
	}
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("original branch = %q, want feature", branch)
	}
}

func TestExtractWorktreeRestoresOriginalWhenCreateFails(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")
	createErr := errors.New("create failed")

	calls := extractCalls{createErr: createErr}
	_, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if !errors.Is(err, createErr) {
		t.Fatalf("ExtractWorktree() error = %v, want create failure", err)
	}
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("original branch = %q, want feature restored", branch)
	}
}

func TestExtractWorktreeRollsBackAndRestoresWhenOpenFails(t *testing.T) {
	repo := testGitRepo(t)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")
	openErr := errors.New("open failed")

	calls := extractCalls{openErr: openErr}
	_, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if !errors.Is(err, openErr) {
		t.Fatalf("ExtractWorktree() error = %v, want open failure", err)
	}
	if len(calls.rolledBack) != 1 {
		t.Fatalf("rollback calls = %#v, want one", calls.rolledBack)
	}
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("original branch = %q, want feature restored", branch)
	}
}

func TestExtractWorktreeRequiresGitAdapter(t *testing.T) {
	_, err := ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{}, ExtractAdapter{})
	if err == nil {
		t.Fatal("ExtractWorktree() error = nil, want git adapter error")
	}
}

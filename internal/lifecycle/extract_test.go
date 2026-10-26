package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/lifecycle"
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

func extractAdapter(gitAdapter *git.Commands, calls *extractCalls) lifecycle.ExtractAdapter {
	return lifecycle.ExtractAdapter{
		WorktreeAdapter: lifecycle.WorktreeAdapter{
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

type realWorktreeAdapter struct {
	openErr error
}

func (a realWorktreeAdapter) create(ctx context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	command := exec.CommandContext(ctx, "git", "-C", options.Repo, "worktree", "add", options.Destination, options.Branch)
	if output, err := command.CombinedOutput(); err != nil {
		return capabilities.Worktree{}, fmt.Errorf("git worktree add: %s: %w", output, err)
	}
	return capabilities.Worktree{Repo: options.Repo, Path: options.Destination, Branch: options.Branch}, nil
}

func (a realWorktreeAdapter) open(_ context.Context, path string, _ capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
	if a.openErr != nil {
		return capabilities.OpenedContainer{}, a.openErr
	}
	return capabilities.OpenedContainer{Path: path, ID: "real", Created: true}, nil
}

func (realWorktreeAdapter) rollback(ctx context.Context, worktree capabilities.Worktree) error {
	command := exec.CommandContext(ctx, "git", "-C", worktree.Repo, "worktree", "remove", "--force", worktree.Path)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", output, err)
	}
	return nil
}

func realExtractAdapter() lifecycle.ExtractAdapter {
	adapter := realWorktreeAdapter{}
	return lifecycle.ExtractAdapter{
		WorktreeAdapter: lifecycle.WorktreeAdapter{Create: adapter.create, Open: adapter.open, Rollback: adapter.rollback},
		Git:             git.New(runner.ExecRunner{}),
	}
}

func TestExtractWorktreeMovesBranchToDestination(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")

	var calls extractCalls
	got, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, NoFocus: true,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err != nil {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v", err)
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
	got, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: "release",
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err != nil {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v", err)
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
	got, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err != nil {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v", err)
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
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want dirty refusal", err)
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
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want existing destination refusal", err)
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
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: "occupied",
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if err == nil || !strings.Contains(err.Error(), "already checked out") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want ownership refusal", err)
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
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if !errors.Is(err, createErr) {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want create failure", err)
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
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination,
	}, extractAdapter(git.New(runner.ExecRunner{}), &calls))
	if !errors.Is(err, openErr) {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want open failure", err)
	}
	if len(calls.rolledBack) != 1 {
		t.Fatalf("rollback calls = %#v, want one", calls.rolledBack)
	}
	if branch := currentBranch(t, repo); branch != "feature" {
		t.Fatalf("original branch = %q, want feature restored", branch)
	}
}

func TestExtractWorktreeRefusesIgnoredOverwriteAndPreservesOriginal(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "secret"), []byte("versioned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", "secret")
	runTestGit(t, repo, "commit", "-qm", "track secret")
	runTestGit(t, repo, "checkout", "-qb", "feature")
	runTestGit(t, repo, "rm", "-q", "secret")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", ".gitignore")
	runTestGit(t, repo, "commit", "-qm", "ignore secret")
	if err := os.WriteFile(filepath.Join(repo, "secret"), []byte("local-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "feature")
	before := runTestGit(t, repo, "rev-parse", "HEAD")

	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base,
	}, realExtractAdapter())
	if err == nil || !strings.Contains(err.Error(), "checkout") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want ignored overwrite refusal", err)
	}
	if currentBranch(t, repo) != "feature" || runTestGit(t, repo, "rev-parse", "HEAD") != before {
		t.Fatalf("original checkout changed after refusal")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after refusal: %v", statErr)
	}
	contents, readErr := os.ReadFile(filepath.Join(repo, "secret"))
	if readErr != nil || string(contents) != "local-only\n" {
		t.Fatalf("ignored local file = %q, %v", contents, readErr)
	}
	if listing := runTestGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(listing, destination) || !strings.Contains(listing, "branch refs/heads/feature") {
		t.Fatalf("worktree list after refusal = %s", listing)
	}
}

func TestExtractWorktreeCleanCheckIgnoresStatusConfiguration(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	runTestGit(t, repo, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(repo, "untracked"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "feature")
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base,
	}, realExtractAdapter())
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want configuration-independent dirty refusal", err)
	}
	if currentBranch(t, repo) != "feature" {
		t.Fatalf("original branch changed after dirty refusal")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after dirty refusal: %v", statErr)
	}
	if listing := runTestGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(listing, destination) {
		t.Fatalf("worktree list after dirty refusal = %s", listing)
	}
}

func TestExtractWorktreeCleanCheckCatchesTrackedChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		staged bool
	}{
		{name: "unstaged"},
		{name: "staged", staged: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := testGitRepo(t)
			base := currentBranch(t, repo)
			runTestGit(t, repo, "checkout", "-qb", "feature")
			if err := os.WriteFile(filepath.Join(repo, "README"), []byte("dirty\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.staged {
				runTestGit(t, repo, "add", "README")
			}
			destination := filepath.Join(t.TempDir(), "feature")
			_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
				Repo: repo, Destination: destination, ReturnBranch: base,
			}, realExtractAdapter())
			if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
				t.Fatalf("lifecycle.ExtractWorktree() error = %v, want dirty refusal", err)
			}
			if currentBranch(t, repo) != "feature" || strings.Contains(runTestGit(t, repo, "worktree", "list", "--porcelain"), destination) {
				t.Fatalf("repository changed after tracked dirty refusal")
			}
		})
	}
}

func TestExtractWorktreeCleanCheckIncludesIgnoredSubmoduleChanges(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	submodule := testGitRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", submodule, "lib").CombinedOutput(); err != nil {
		t.Fatalf("add submodule: %v (%s)", err, output)
	}
	runTestGit(t, repo, "commit", "-qam", "add submodule")
	runTestGit(t, repo, "checkout", "-qb", "feature")
	runTestGit(t, repo, "config", "submodule.lib.ignore", "all")
	if err := os.WriteFile(filepath.Join(repo, "lib", "README"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "feature")
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base,
	}, realExtractAdapter())
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want ignored submodule refusal", err)
	}
	if currentBranch(t, repo) != "feature" || strings.Contains(runTestGit(t, repo, "worktree", "list", "--porcelain"), destination) {
		t.Fatalf("repository changed after ignored submodule refusal")
	}
}

func TestExtractWorktreeRestoresAfterInitialCheckoutHookFailure(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	before := runTestGit(t, repo, "rev-parse", "HEAD")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "feature")
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base,
	}, realExtractAdapter())
	if err == nil {
		t.Fatal("lifecycle.ExtractWorktree() error = nil, want hook failure")
	}
	if currentBranch(t, repo) != "feature" || runTestGit(t, repo, "rev-parse", "HEAD") != before {
		t.Fatalf("original checkout was not restored after hook failure")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after hook failure: %v", statErr)
	}
	if listing := runTestGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(listing, destination) || !strings.Contains(listing, "branch refs/heads/feature") {
		t.Fatalf("worktree list after hook failure = %s", listing)
	}
}

func TestExtractWorktreeRejectsNestedDestination(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(repo, "nested", "feature")
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base,
	}, realExtractAdapter())
	if err == nil || !strings.Contains(err.Error(), "inside registered worktree") {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want nested destination refusal", err)
	}
	if currentBranch(t, repo) != "feature" || strings.Contains(runTestGit(t, repo, "worktree", "list", "--porcelain"), destination) {
		t.Fatalf("repository changed after nested destination refusal")
	}
}

func TestExtractWorktreeCleanupSurvivesCancellationAfterCreate(t *testing.T) {
	repo := testGitRepo(t)
	base := currentBranch(t, repo)
	runTestGit(t, repo, "checkout", "-qb", "feature")
	destination := filepath.Join(t.TempDir(), "feature")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := realWorktreeAdapter{}
	openErr := errors.New("open canceled")
	gotAdapter := lifecycle.ExtractAdapter{
		WorktreeAdapter: lifecycle.WorktreeAdapter{
			Create: adapter.create,
			Open: func(_ context.Context, path string, _ capabilities.OpenOptions) (capabilities.OpenedContainer, error) {
				cancel()
				return capabilities.OpenedContainer{Path: path}, openErr
			},
			Rollback: adapter.rollback,
		},
		Git: git.New(runner.ExecRunner{}),
	}
	_, err := lifecycle.ExtractWorktree(ctx, capabilities.WorktreeExtractOptions{
		Repo: repo, Destination: destination, ReturnBranch: base,
	}, gotAdapter)
	if !errors.Is(err, openErr) {
		t.Fatalf("lifecycle.ExtractWorktree() error = %v, want open cancellation error", err)
	}
	if currentBranch(t, repo) != "feature" {
		t.Fatalf("original branch = %q, want feature", currentBranch(t, repo))
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination after canceled cleanup = %v", statErr)
	}
	listing := runTestGit(t, repo, "worktree", "list", "--porcelain")
	if strings.Contains(listing, destination) || !strings.Contains(listing, "branch refs/heads/feature") {
		t.Fatalf("worktree list after canceled cleanup = %s", listing)
	}
}

func TestExtractWorktreeRequiresGitAdapter(t *testing.T) {
	_, err := lifecycle.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{}, lifecycle.ExtractAdapter{})
	if err == nil {
		t.Fatal("lifecycle.ExtractWorktree() error = nil, want git adapter error")
	}
}

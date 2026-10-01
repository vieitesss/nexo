package deletion

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

func TestGitInspectionFailureWithMetadataIsHardError(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (GitInspector{Runner: failingGitRunner{}}).Inspect(context.Background(), root)
	if err == nil {
		t.Fatal("Inspect() error = nil for unreadable repository metadata")
	}
}

func TestGitInspectionFailureWithoutMetadataIsNonRepository(t *testing.T) {
	root := t.TempDir()
	state, err := (GitInspector{Runner: failingGitRunner{}}).Inspect(context.Background(), root)
	if err != nil || state.IsRepository {
		t.Fatalf("Inspect() = %#v, %v; want confirmed non-repository", state, err)
	}
}

type failingGitRunner struct{}

func (failingGitRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("git unavailable")
}

func TestExecuteStopsWhenIdentityChangesAtMutationBoundary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Path: path, Action: "trash", Fingerprint: fingerprint, CanProceed: true}
	fs := &raceStageFS{path: path}
	trash := countingTrash{}
	if err := Execute(context.Background(), plan, true, false, fs, nil, trash); err == nil {
		t.Fatal("Execute() error = nil after staged identity replacement")
	}
	if trash.calls != 0 {
		t.Fatalf("trash calls = %d, want none", trash.calls)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("replacement path missing: %v", err)
	}
}

type raceStageFS struct {
	OSFileSystem
	path string
}

func (f *raceStageFS) Stage(path, _ string) (string, error) {
	if path != f.path {
		return "", errors.New("unexpected stage path")
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return "", err
	}
	return "", errors.New("identity changed while staging")
}

func (f *raceStageFS) Restore(string, string) error { return nil }

type countingTrash struct{ calls int }

func (t countingTrash) Trash(context.Context, string) error {
	return errors.New("unexpected trash")
}

func TestGitDryRunObservationDoesNotRewriteIndex(t *testing.T) {
	setDeletionEnv(t)
	repo := t.TempDir()
	gitCommand(t, repo, "init", "-q")
	gitCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "tracked")
	gitCommand(t, repo, "commit", "-qm", "initial")
	before := fileDigest(t, filepath.Join(repo, ".git", "index"))
	if _, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	after := fileDigest(t, filepath.Join(repo, ".git", "index"))
	if before != after {
		t.Fatalf("index digest changed from %s to %s", before, after)
	}
}

func TestLinkedWorktreeRefusalFactsBecomeBlockers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "linked")
	script := scriptedGitRunner{outputs: map[string][]byte{
		"rev-parse --git-dir":          []byte("/repo/.git/worktrees/linked\n"),
		"rev-parse --git-common-dir":   []byte("/repo/.git\n"),
		"rev-parse --show-toplevel":    []byte(path + "\n"),
		"worktree list --porcelain":    []byte("worktree /repo\n\nworktree " + path + "\nlocked\n\n"),
		"status --porcelain=v2":        []byte("1 .M N... 100644 100644 100644 a a tracked\n? untracked\n"),
		"for-each-ref":                 []byte("main\t\n"),
		"remote":                       []byte(""),
		"rev-parse --verify --quiet":   []byte("commit\n"),
		"submodule status --recursive": []byte("-deadbeef submodule\n"),
	}}
	state, err := (GitInspector{Runner: script}).Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	plan := Preflight(Observation{Path: path, Candidate: true, Configured: true, Exists: true, Directory: true, Git: state})
	for _, code := range []string{"worktree-locked", "worktree-changed", "worktree-submodules"} {
		if !hasCode(plan.Blockers, code) {
			t.Fatalf("plan = %#v, missing %s", plan, code)
		}
	}
	if plan.CanProceed {
		t.Fatal("linked worktree refusal facts were overridable")
	}
}

type scriptedGitRunner struct {
	outputs map[string][]byte
}

func (r scriptedGitRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	for key, output := range r.outputs {
		if strings.Contains(joined, key) {
			return output, nil
		}
	}
	if strings.Contains(joined, "rev-parse --verify --quiet refs/stash") {
		return nil, errors.New("no stash")
	}
	return nil, fmt.Errorf("unexpected git command %q", joined)
}

func TestAllLocalBranchesAndRemovedRemoteRefAreReported(t *testing.T) {
	setDeletionEnv(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, bare, "init", "--bare", "-q")
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "init", "-q")
	gitCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "tracked")
	gitCommand(t, repo, "commit", "-qm", "initial")
	branch := strings.TrimSpace(string(gitOutput(t, repo, "branch", "--show-current")))
	gitCommand(t, repo, "remote", "add", "origin", bare)
	gitCommand(t, repo, "push", "-q", "-u", "origin", branch)
	gitCommand(t, repo, "switch", "-c", "local-only")
	if err := os.WriteFile(filepath.Join(repo, "local"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "add", "local")
	gitCommand(t, repo, "commit", "-qm", "local")
	gitCommand(t, repo, "switch", "-q", branch)
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unpushed {
		t.Fatalf("state = %#v, want local-only branch warning", state)
	}

	gitCommand(t, repo, "switch", "-c", "published")
	gitCommand(t, repo, "push", "-q", "-u", "origin", "published")
	gitCommand(t, repo, "switch", "-q", branch)
	gitCommand(t, bare, "update-ref", "-d", "refs/heads/published")
	state, err = (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unpushed {
		t.Fatalf("state = %#v, want removed remote ref warning", state)
	}
}

func gitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return output
}

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(contents)
}

func setDeletionEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

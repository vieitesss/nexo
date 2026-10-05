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
	"time"

	"github.com/vieitesss/nexo/internal/runner"
)

func TestGitInspectionFailuresWithMetadataAreHardErrors(t *testing.T) {
	setGitTestEnv(t)
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "unavailable", err: errors.New("git unavailable")},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "ownership", err: errors.New("dubious ownership")},
		{name: "corrupt metadata", err: errors.New("bad repository format")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := (GitInspector{Runner: failingGitRunner{err: test.err}}).Inspect(context.Background(), root)
			if err == nil {
				t.Fatalf("Inspect() error = nil for %s", test.name)
			}
		})
	}
}

func TestGitInspectionFailureWithoutMetadataIsNonRepository(t *testing.T) {
	setGitTestEnv(t)
	root := t.TempDir()
	state, err := (GitInspector{Runner: failingGitRunner{err: errors.New("git unavailable")}}).Inspect(context.Background(), root)
	if err != nil || state.IsRepository {
		t.Fatalf("Inspect() = %#v, %v; want confirmed non-repository", state, err)
	}
}

type failingGitRunner struct{ err error }

func (f failingGitRunner) Run(context.Context, string, ...string) ([]byte, error) {
	if f.err == nil {
		return nil, errors.New("git unavailable")
	}
	return nil, f.err
}

func TestGitDryRunObservationDoesNotRewriteIndex(t *testing.T) {
	setGitTestEnv(t)
	repo := gitRegressionRepo(t)
	indexPath := filepath.Join(repo, ".git", "index")
	beforeDigest := fileDigest(t, indexPath)
	beforeInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(repo, "tracked")
	if err := os.Chtimes(tracked, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	afterDigest := fileDigest(t, indexPath)
	afterInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if beforeDigest != afterDigest || beforeInfo.Size() != afterInfo.Size() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("index metadata changed: digest %x -> %x, info %#v -> %#v", beforeDigest, afterDigest, beforeInfo, afterInfo)
	}
}

func TestLinkedWorktreeRefusalFactsBecomeBlockers(t *testing.T) {
	setGitTestEnv(t)
	path := filepath.Join(t.TempDir(), "linked")
	script := scriptedGitRunner{outputs: map[string][]byte{
		"rev-parse --git-dir":          []byte("/repo/.git/worktrees/linked\n"),
		"rev-parse --git-common-dir":   []byte("/repo/.git\n"),
		"rev-parse --show-toplevel":    []byte(path + "\n"),
		"worktree list --porcelain":    []byte("worktree /repo\n\nworktree " + path + "\nlocked\n\n"),
		"status --porcelain=v2":        []byte("1 .M N... 100644 100644 100644 a a tracked\n? untracked\n"),
		"for-each-ref":                 []byte(""),
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
	return nil, fmt.Errorf("unexpected git command %q", joined)
}

func TestExecuteRealLinkedWorktreeUsesNonForceGitRemoval(t *testing.T) {
	setGitTestEnv(t)
	root := realTempDir(t)
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "init", "-q")
	gitRegressionCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitRegressionCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "add", "tracked")
	gitRegressionCommand(t, repo, "commit", "-qm", "initial")
	linked := filepath.Join(root, "linked")
	gitRegressionCommand(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LinkedWorktree || state.PrimaryPath == "" {
		t.Fatalf("linked state = %#v", state)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, linked)
	if err != nil {
		t.Fatal(err)
	}
	repositoryFingerprint, err := Fingerprint(OSFileSystem{}, state.PrimaryPath)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Path: linked, Action: "worktree", Repository: state.PrimaryPath, Fingerprint: fingerprint, RepositoryFingerprint: repositoryFingerprint, CanProceed: true}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, GitInspector{Runner: runner.ExecRunner{}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(linked); !os.IsNotExist(err) {
		t.Fatalf("linked worktree remains: %v", err)
	}
	listing := string(gitRegressionOutput(t, repo, "worktree", "list", "--porcelain"))
	if strings.Contains(listing, linked) {
		t.Fatalf("worktree registration remains: %s", listing)
	}
}

func TestGitInspectorReadsRealWorktreeRefusalFacts(t *testing.T) {
	setGitTestEnv(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "init", "-q")
	gitRegressionCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitRegressionCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "add", "tracked")
	gitRegressionCommand(t, repo, "commit", "-qm", "initial")
	linked := filepath.Join(root, "linked")
	gitRegressionCommand(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	gitRegressionCommand(t, repo, "worktree", "lock", linked)
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LinkedWorktree || !state.WorktreeLocked {
		t.Fatalf("locked worktree state = %#v", state)
	}
	gitRegressionCommand(t, repo, "worktree", "unlock", linked)
	if err := os.WriteFile(filepath.Join(linked, "untracked"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err = (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Dirty || !state.Untracked {
		t.Fatalf("dirty worktree state = %#v", state)
	}
	if err := os.Remove(filepath.Join(linked, "untracked")); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "worktree", "remove", linked)

	subrepo := filepath.Join(root, "subrepo")
	if err := os.Mkdir(subrepo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, subrepo, "init", "-q")
	gitRegressionCommand(t, subrepo, "config", "user.email", "test@example.invalid")
	gitRegressionCommand(t, subrepo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(subrepo, "subtracked"), []byte("sub"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, subrepo, "add", "subtracked")
	gitRegressionCommand(t, subrepo, "commit", "-qm", "sub initial")
	gitRegressionCommand(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subrepo, "sub")
	gitRegressionCommand(t, repo, "commit", "-qam", "add submodule")
	linked = filepath.Join(root, "linked-submodule")
	gitRegressionCommand(t, repo, "worktree", "add", "-q", "-b", "linked-submodule", linked)
	state, err = (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasSubmodules {
		t.Fatalf("submodule worktree state = %#v", state)
	}
}

func TestAllLocalBranchesAndRemovedRemoteRefAreReported(t *testing.T) {
	setGitTestEnv(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, bare, "init", "--bare", "-q")
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "init", "-q")
	gitRegressionCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitRegressionCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "add", "tracked")
	gitRegressionCommand(t, repo, "commit", "-qm", "initial")
	branch := strings.TrimSpace(string(gitRegressionOutput(t, repo, "branch", "--show-current")))
	gitRegressionCommand(t, repo, "remote", "add", "origin", bare)
	gitRegressionCommand(t, repo, "push", "-q", "-u", "origin", branch)
	gitRegressionCommand(t, repo, "switch", "-c", "local-only")
	if err := os.WriteFile(filepath.Join(repo, "local"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "add", "local")
	gitRegressionCommand(t, repo, "commit", "-qm", "local")
	gitRegressionCommand(t, repo, "switch", "-q", branch)
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unpushed {
		t.Fatalf("state = %#v, want local-only branch warning", state)
	}
	gitRegressionCommand(t, repo, "branch", "-D", "local-only")

	gitRegressionCommand(t, repo, "switch", "-c", "published")
	gitRegressionCommand(t, repo, "push", "-q", "-u", "origin", "published")
	gitRegressionCommand(t, repo, "switch", "-q", branch)
	gitRegressionCommand(t, bare, "update-ref", "-d", "refs/heads/published")
	state, err = (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unpushed {
		t.Fatalf("state = %#v, want removed remote ref warning", state)
	}
}

func TestDetachedHeadUnpublishedCommitIsReported(t *testing.T) {
	setGitTestEnv(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, bare, "init", "--bare", "-q")
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "init", "-q")
	gitRegressionCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitRegressionCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "add", "tracked")
	gitRegressionCommand(t, repo, "commit", "-qm", "initial")
	branch := strings.TrimSpace(string(gitRegressionOutput(t, repo, "branch", "--show-current")))
	gitRegressionCommand(t, repo, "remote", "add", "origin", bare)
	gitRegressionCommand(t, repo, "push", "-q", "-u", "origin", branch)
	gitRegressionCommand(t, repo, "switch", "--detach", "-q", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "commit", "-am", "detached unpublished")
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unpushed {
		t.Fatalf("state = %#v, want detached unpublished commit warning", state)
	}
}

func TestExecuteRejectsChangedWorktreeAncestorBeforeGit(t *testing.T) {
	setGitTestEnv(t)
	root := t.TempDir()
	parent := filepath.Join(root, "group")
	path := filepath.Join(parent, "linked")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "moved")
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, parent); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	git := &recordingGitRemoval{}
	plan := Plan{Path: path, Action: "worktree", Repository: filepath.Join(root, "repo"), Fingerprint: fingerprint, CanProceed: true}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, git, nil); err == nil {
		t.Fatal("Execute() error = nil through replaced ancestor")
	}
	if git.calls != 0 {
		t.Fatal("Git removal ran through a changed ancestor")
	}
	if _, err := os.Lstat(filepath.Join(moved, "linked")); err != nil {
		t.Fatalf("moved worktree changed: %v", err)
	}
}

type recordingGitRemoval struct{ calls int }

func (g *recordingGitRemoval) RemoveWorktree(context.Context, string, string) error {
	g.calls++
	return nil
}

func gitRegressionRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitRegressionCommand(t, repo, "init", "-q")
	gitRegressionCommand(t, repo, "config", "user.email", "test@example.invalid")
	gitRegressionCommand(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRegressionCommand(t, repo, "add", "tracked")
	gitRegressionCommand(t, repo, "commit", "-qm", "initial")
	return repo
}

func gitRegressionCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitRegressionOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = os.Environ()
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

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

func TestGitInspectionMetadataFailuresStayHardBlockers(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
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

type failingGitRunner struct{ err error }

func (f failingGitRunner) Run(context.Context, string, ...string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
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
	trash := &countingTrash{}
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

type identityTrashRecorder struct {
	staged   string
	original string
}

func (t *identityTrashRecorder) Trash(context.Context, string) error {
	return errors.New("identity-aware trash was not used")
}

func (t *identityTrashRecorder) TrashEntry(_ context.Context, staged, original string) error {
	t.staged, t.original = staged, original
	return os.RemoveAll(staged)
}

func TestExecutePreservesOriginalTrashIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "payments")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	trash := &identityTrashRecorder{}
	plan := Plan{Path: path, Action: "trash", Fingerprint: fingerprint, CanProceed: true}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, nil, trash); err != nil {
		t.Fatal(err)
	}
	if trash.original != path || filepath.Base(trash.staged) != filepath.Base(path) {
		t.Fatalf("trash identity = staged %q original %q, want original basename and path %q", trash.staged, trash.original, path)
	}
}

type countingTrash struct{ calls int }

func (t *countingTrash) Trash(context.Context, string) error {
	t.calls++
	return errors.New("unexpected trash")
}

func TestExecuteWorktreeUsesIdentityBoundStage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "linked")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	git := &recordingGitRemoval{}
	plan := Plan{Path: path, Action: "worktree", Repository: filepath.Join(root, "repo"), Fingerprint: fingerprint, CanProceed: true}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, git, nil); err != nil {
		t.Fatal(err)
	}
	if git.path != path || git.calls != 1 {
		t.Fatalf("git removal = %#v, want original operand once", git)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("linked worktree remains after removal: %v", err)
	}
}

type recordingGitRemoval struct {
	calls int
	path  string
}

func (g *recordingGitRemoval) RemoveWorktree(_ context.Context, _ string, path string) error {
	g.calls++
	g.path = path
	return nil
}

func TestExecuteRejectsChangedWorktreeAncestorBeforeGit(t *testing.T) {
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

func TestExecuteReportsStagedWorktreeWhenReplacementBlocksRestore(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "linked")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(root, ".nexo-delete-staged")
	fs := &replacementStageFS{OSFileSystem: OSFileSystem{}, path: path, staged: staged}
	git := &recordingGitRemoval{}
	plan := Plan{Path: path, Action: "worktree", Repository: filepath.Join(root, "repo"), Fingerprint: fingerprint, CanProceed: true}
	err = Execute(context.Background(), plan, true, false, fs, git, nil)
	if err == nil || !strings.Contains(err.Error(), staged) {
		t.Fatalf("Execute() = %v, want recoverable staged path", err)
	}
	if git.calls != 0 {
		t.Fatal("Git removal ran while staged replacement was unresolved")
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("replacement path missing: %v", statErr)
	}
	if _, statErr := os.Lstat(staged); statErr != nil {
		t.Fatalf("staged selected inode missing: %v", statErr)
	}
}

type replacementStageFS struct {
	OSFileSystem
	path   string
	staged string
}

func (f *replacementStageFS) Stage(path, _ string) (string, error) {
	if err := os.Rename(path, f.staged); err != nil {
		return "", err
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return f.staged, err
	}
	return f.staged, errors.New("replacement installed while staging")
}

func TestExecuteReportsStagedPathWhenReplacementBlocksRestore(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(root, ".nexo-delete-staged")
	fs := &replacementStageFS{OSFileSystem: OSFileSystem{}, path: path, staged: staged}
	plan := Plan{Path: path, Action: "trash", Fingerprint: fingerprint, CanProceed: true}
	err = Execute(context.Background(), plan, true, false, fs, nil, &countingTrash{})
	if err == nil || !strings.Contains(err.Error(), staged) {
		t.Fatalf("Execute() = %v, want recoverable staged path", err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("replacement path missing: %v", statErr)
	}
	if _, statErr := os.Lstat(staged); statErr != nil {
		t.Fatalf("staged selected inode missing: %v", statErr)
	}
}

func TestExecuteRestoresPathWhenStageVerificationFails(t *testing.T) {
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
	fs := &postRenameFailureFS{OSFileSystem: OSFileSystem{}, path: path, staged: filepath.Join(root, ".nexo-delete-staged")}
	if err := Execute(context.Background(), plan, true, false, fs, nil, &countingTrash{}); err == nil {
		t.Fatal("Execute() error = nil after stage verification failure")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("original path was not restored: %v", err)
	}
	if _, err := os.Lstat(fs.staged); !os.IsNotExist(err) {
		t.Fatalf("staged path remains after rollback: %v", err)
	}
}

type postRenameFailureFS struct {
	OSFileSystem
	path   string
	staged string
}

func (f *postRenameFailureFS) Stage(path, _ string) (string, error) {
	if path != f.path {
		return "", errors.New("unexpected stage path")
	}
	if err := os.Rename(path, f.staged); err != nil {
		return "", err
	}
	return f.staged, errors.New("identity changed while staging")
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
	indexPath := filepath.Join(repo, ".git", "index")
	before := fileDigest(t, indexPath)
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
	after := fileDigest(t, indexPath)
	afterInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || beforeInfo.Size() != afterInfo.Size() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("index metadata changed: digest %s -> %s, info %#v -> %#v", before, after, beforeInfo, afterInfo)
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

func TestExecuteRealLinkedWorktreeUsesNonForceGitRemoval(t *testing.T) {
	setDeletionEnv(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
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
	linked := filepath.Join(root, "linked")
	gitCommand(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
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
	listing := string(gitOutput(t, repo, "worktree", "list", "--porcelain"))
	if strings.Contains(listing, linked) {
		t.Fatalf("worktree registration remains: %s", listing)
	}
}

func TestExecuteRejectsChangedGitRepositoryIdentity(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	linked := filepath.Join(root, "linked")
	if err := os.Mkdir(repo, 0o755); err != nil {
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
	gitCommand(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	pathFingerprint, err := Fingerprint(OSFileSystem{}, linked)
	if err != nil {
		t.Fatal(err)
	}
	repositoryFingerprint, err := Fingerprint(OSFileSystem{}, state.PrimaryPath)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "moved-repo")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, repo); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	plan := Plan{Path: linked, Action: "worktree", Repository: repo, Fingerprint: pathFingerprint, RepositoryFingerprint: repositoryFingerprint, CanProceed: true}
	git := &recordingGitRemoval{}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, git, nil); err == nil {
		t.Fatal("Execute() error = nil after Git repository replacement")
	}
	if git.calls != 0 {
		t.Fatal("Git removal ran against a replacement repository")
	}
}

func TestGitInspectorReadsRealWorktreeRefusalFacts(t *testing.T) {
	setDeletionEnv(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
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
	linked := filepath.Join(root, "linked")
	gitCommand(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	gitCommand(t, repo, "worktree", "lock", linked)
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LinkedWorktree || !state.WorktreeLocked {
		t.Fatalf("locked worktree state = %#v", state)
	}
	gitCommand(t, repo, "worktree", "unlock", linked)
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
	gitCommand(t, repo, "worktree", "remove", linked)

	subrepo := filepath.Join(root, "subrepo")
	if err := os.Mkdir(subrepo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, subrepo, "init", "-q")
	gitCommand(t, subrepo, "config", "user.email", "test@example.invalid")
	gitCommand(t, subrepo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(subrepo, "subtracked"), []byte("sub"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, subrepo, "add", "subtracked")
	gitCommand(t, subrepo, "commit", "-qm", "sub initial")
	gitCommand(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subrepo, "sub")
	gitCommand(t, repo, "commit", "-qam", "add submodule")
	linked = filepath.Join(root, "linked-submodule")
	gitCommand(t, repo, "worktree", "add", "-q", "-b", "linked-submodule", linked)
	state, err = (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasSubmodules {
		t.Fatalf("submodule worktree state = %#v", state)
	}
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
	gitCommand(t, repo, "branch", "-D", "local-only")
	state, err = (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if state.Unpushed {
		t.Fatalf("state = %#v, local-only branch was removed", state)
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

func TestDetachedHeadUnpublishedCommitIsReported(t *testing.T) {
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
	gitCommand(t, repo, "switch", "--detach", "-q", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, repo, "commit", "-am", "detached unpublished")
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unpushed {
		t.Fatalf("state = %#v, want detached unpublished commit warning", state)
	}
}

func TestSeparateGitDirStandaloneIsNotLinkedWorktree(t *testing.T) {
	setDeletionEnv(t)
	metadata := filepath.Join(t.TempDir(), "metadata.git")
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, checkout, "init", "--separate-git-dir", metadata, "-q")
	gitCommand(t, checkout, "config", "user.email", "test@example.invalid")
	gitCommand(t, checkout, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(checkout, "tracked"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, checkout, "add", "tracked")
	gitCommand(t, checkout, "commit", "-qm", "initial")
	state, err := (GitInspector{Runner: runner.ExecRunner{}}).Inspect(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsRepository || state.LinkedWorktree || state.PrimaryPath != "" {
		t.Fatalf("state = %#v, want standalone separate-metadata repository", state)
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

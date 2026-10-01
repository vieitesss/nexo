package deletion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightClassifiesWarningsAndBlockersWithoutMutation(t *testing.T) {
	plan := Preflight(Observation{
		Path: "/tmp/project", Candidate: true, Configured: true, Exists: true, Directory: true,
		NonEmpty: true, Fingerprint: "one", CurrentFingerprint: "two",
		Git: GitState{IsRepository: true, RemoteChecked: true, Dirty: true, Untracked: true, Unpushed: true, Stashes: true},
	})
	if plan.CanProceed || len(plan.Blockers) != 1 || plan.Blockers[0].Code != "path-changed" {
		t.Fatalf("plan = %#v, want one hard path blocker", plan)
	}
	if len(plan.Warnings) != 2 {
		t.Fatalf("warnings = %#v, want directory and Git warnings", plan.Warnings)
	}
}

func TestPreflightSymlinkIsLinkAndNeverWorktree(t *testing.T) {
	plan := Preflight(Observation{Path: "/tmp/link", Candidate: true, Configured: true, Exists: true, Symlink: true, Git: GitState{IsRepository: true, LinkedWorktree: true}})
	if !plan.Symlink || plan.Action != "trash" || !plan.CanProceed {
		t.Fatalf("plan = %#v, want safe link trash", plan)
	}
}

func TestInspectFilesystemRejectsSymlinkAncestors(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realRoot, "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	observation, err := InspectFilesystem(OSFileSystem{}, filepath.Join(alias, "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	plan := Preflight(observation)
	if !hasCode(plan.Blockers, "ancestor-check-failed") {
		t.Fatalf("plan = %#v, want ancestor safety blocker", plan)
	}
}

func TestPreflightPrimaryDependentWorktreeIsHardBlocker(t *testing.T) {
	plan := Preflight(Observation{Path: "/tmp/repo", Candidate: true, Configured: true, Exists: true, Directory: true,
		Git: GitState{IsRepository: true, RemoteChecked: true, DependentWorktrees: []string{"/tmp/repo-wt"}}})
	if plan.CanProceed || !hasCode(plan.Blockers, "dependent-worktrees") {
		t.Fatalf("plan = %#v, want dependent-worktrees blocker", plan)
	}
}

func TestExecuteUsesTrashWithoutPermanentFallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "repo")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	trashErr := errors.New("trash unavailable")
	trash := fakeTrash{err: trashErr}
	plan, err := inspectPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, nil, trash); !errors.Is(err, trashErr) {
		t.Fatalf("Execute() = %v, want trash error", err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("trash failure removed path: %v", statErr)
	}
}

func TestExecutePermanentRemovesSymlinkOnly(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	plan, err := inspectPlan(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(context.Background(), plan, false, true, OSFileSystem{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link remains: %v", err)
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("target was followed/deleted: %v", err)
	}
}

func inspectPlan(path string) (Plan, error) {
	observation, err := InspectFilesystem(OSFileSystem{}, path)
	if err != nil {
		return Plan{}, err
	}
	observation.Candidate, observation.Configured = true, true
	return Preflight(observation), nil
}

func hasCode(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

type fakeTrash struct{ err error }

func (f fakeTrash) Trash(context.Context, string) error { return f.err }

func TestCommandTrashDoesNotPermanentDeleteWhenUtilitiesFail(t *testing.T) {
	runner := &trashRunner{err: errors.New("missing")}
	err := (CommandTrash{Runner: runner, GOOS: "linux"}).Trash(context.Background(), "/tmp/repo")
	if err == nil || !strings.Contains(err.Error(), "trash") {
		t.Fatalf("Trash() = %v, want error", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %#v, want gio and trash-put only", runner.calls)
	}
}

type trashRunner struct {
	calls [][]string
	err   error
}

func (r *trashRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, r.err
}

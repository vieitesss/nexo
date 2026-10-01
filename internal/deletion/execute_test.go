package deletion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type worktreeRecorder struct {
	repo  string
	path  string
	calls int
}

type trashRecorder struct {
	path string
}

func (r *trashRecorder) Trash(_ context.Context, path string) error {
	r.path = path
	return nil
}

func (r *worktreeRecorder) RemoveWorktree(_ context.Context, repo, path string) error {
	r.repo, r.path, r.calls = repo, path, r.calls+1
	return nil
}

func TestExecuteTrashUsesOriginalLexicalPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "project ")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, path)
	if err != nil {
		t.Fatal(err)
	}
	trash := &trashRecorder{}
	plan := Plan{Path: path, Action: "trash", Fingerprint: fingerprint, CanProceed: true}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, nil, trash); err != nil {
		t.Fatal(err)
	}
	if trash.path != path {
		t.Fatalf("trash path = %q, want exact lexical path %q", trash.path, path)
	}
}

func TestExecuteLinkedWorktreeUsesRegisteredPathWithoutStaging(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "primary")
	linked := filepath.Join(root, "linked")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(OSFileSystem{}, linked)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &worktreeRecorder{}
	plan := Plan{Path: linked, Action: "worktree", Repository: repo, Fingerprint: fingerprint, CanProceed: true}
	if err := Execute(context.Background(), plan, true, false, OSFileSystem{}, recorder, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || recorder.repo != repo || recorder.path != linked {
		t.Fatalf("Git removal = %#v, want one call on registered paths", recorder)
	}
	if _, err := Fingerprint(OSFileSystem{}, linked); err != nil {
		t.Fatalf("fake Git removal unexpectedly changed checkout: %v", err)
	}
}

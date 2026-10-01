package herdr

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestCreateWorktreeUsesNativeHerdrOperation(t *testing.T) {
	runner := &fakeRunner{}
	destination := filepath.Join(t.TempDir(), "feature")
	got, err := New(runner).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        "/repo",
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
	})
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if got.Path != destination || got.Branch != "feature" {
		t.Fatalf("result = %#v", got)
	}
	want := []call{{name: "herdr", args: []string{"worktree", "create", "--cwd", "/repo", "--branch", "feature", "--base", "HEAD", "--path", destination, "--focus"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCreateWorktreeUsesExistingHerdrBranch(t *testing.T) {
	runner := &fakeRunner{}
	destination := filepath.Join(t.TempDir(), "feature")
	if _, err := New(runner).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: "/repo", Branch: "feature", Destination: destination,
	}); err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	want := []call{{name: "herdr", args: []string{"worktree", "create", "--cwd", "/repo", "--branch", "feature", "--path", destination, "--focus"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCreateWorktreeNoFocusOmitsFocusFlag(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	runner := &fakeRunner{outputs: map[string][]byte{
		"worktree create --cwd /repo --branch feature --base HEAD --path " + destination: []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"worktree-42","label":"worktrees_feature"},"root_pane":{"pane_id":"p1"}}}`),
	}}
	got, err := New(runner).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        "/repo",
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
		NoFocus:     true,
	})
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	want := []call{{name: "herdr", args: []string{"worktree", "create", "--cwd", "/repo", "--branch", "feature", "--base", "HEAD", "--path", destination}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want no --focus: %#v", runner.calls, want)
	}
	if got.ContainerID != "worktree-42" || got.Container.ID != "worktree-42" || got.Container.Name != "worktrees_feature" || !got.Container.Created || got.Container.Path != destination {
		t.Fatalf("container = %#v, want created worktree container with id and label", got.Container)
	}
}

func TestCreateWorktreeRejectsMissingWorkspaceID(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	runner := &fakeRunner{outputs: map[string][]byte{
		"worktree create --cwd /repo --branch feature --base HEAD --path " + destination + " --focus": []byte(`{"result":{"type":"workspace_created","workspace":{}}}`),
	}}
	if _, err := New(runner).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: "/repo", Branch: "feature", Destination: destination, NewBranch: true,
	}); err == nil {
		t.Fatal("CreateWorktree() error = nil, want missing workspace id error")
	}
}

func TestCreateWorktreeRejectsMalformedCreateOutput(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	runner := &fakeRunner{outputs: map[string][]byte{
		"worktree create --cwd /repo --branch feature --base HEAD --path " + destination + " --focus": []byte("not json"),
	}}
	if _, err := New(runner).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo: "/repo", Branch: "feature", Destination: destination, NewBranch: true,
	}); err == nil {
		t.Fatal("CreateWorktree() error = nil, want malformed output error")
	}
}

func TestCreateExistingWorktreeDestinationFailsBeforeRunner(t *testing.T) {
	runner := &fakeRunner{}
	destination := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := New(runner).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        "/repo",
		Branch:      "feature",
		Destination: destination,
	})
	if err == nil {
		t.Fatal("CreateWorktree() error = nil, want existing destination error")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
}

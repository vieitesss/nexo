package tmux

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
)

func TestCreateWorktreeAddsAndOpensInsideTmux(t *testing.T) {
	runner := &fakeRunner{fail: map[string]error{"-C /repo show-ref --verify --quiet refs/heads/feature": errors.New("missing branch")}}
	destination := filepath.Join(t.TempDir(), "feature")
	got, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
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
	want := []call{
		{name: "git", args: []string{"-C", "/repo", "show-ref", "--verify", "--quiet", "refs/heads/feature"}},
		{name: "git", args: []string{"-C", "/repo", "worktree", "add", "-b", "feature", destination}},
		{name: "tmux", args: []string{"-u", "has-session", "-t", "=" + projects.TargetName(destination)}},
		{name: "tmux", args: []string{"-u", "switch-client", "-t", "=" + projects.TargetName(destination)}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCreateWorktreeRollsBackWhenOpeningFails(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	wantErr := errors.New("switch failed")
	runner := &fakeRunner{fail: map[string]error{
		"-C /repo show-ref --verify --quiet refs/heads/feature": errors.New("missing branch"),
		"switch-client -t =" + projects.TargetName(destination): wantErr,
	}}
	_, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        "/repo",
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateWorktree() error = %v, want %v", err, wantErr)
	}
	if len(runner.calls) != 5 || runner.calls[4].name != "git" || runner.calls[4].args[2] != "show-ref" {
		t.Fatalf("rollback calls = %#v", runner.calls)
	}
}

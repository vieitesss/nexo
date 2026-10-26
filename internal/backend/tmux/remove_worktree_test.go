package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestRemoveWorktreeKillsCreatedSessionBeforeCleanupAndJoinsErrors(t *testing.T) {
	runner := &fakeRunner{fail: map[string]error{
		"kill-session -t =created":                      errors.New("kill failed"),
		"-C /repo worktree remove --force /tmp/created": errors.New("worktree failed"),
		"-C /repo branch -D feature":                    errors.New("branch failed"),
	}}
	err := New(runner).RemoveWorktree(context.Background(), capabilities.Worktree{
		Repo: "/repo", Path: "/tmp/created", Branch: "feature", NewBranch: true, SessionName: "created",
	})
	if err == nil || !strings.Contains(err.Error(), "kill failed") || !strings.Contains(err.Error(), "worktree failed") || !strings.Contains(err.Error(), "branch failed") {
		t.Fatalf("RemoveWorktree() error = %v, want all cleanup failures", err)
	}
	if calls := runner.calls; len(calls) != 3 || calls[0].name != "tmux" || calls[0].args[1] != "kill-session" {
		t.Fatalf("calls = %#v, want the session kill before the shared Git cleanup", calls)
	}
}

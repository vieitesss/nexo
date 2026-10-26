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

// nativeCalls keeps only the tmux commands. The shared Git transaction owns
// the raw-Git sequence and is tested in internal/git; these tests assert the
// session wiring the adapter is responsible for.
func nativeCalls(calls []call) []call {
	native := make([]call, 0, len(calls))
	for _, command := range calls {
		if command.name == "tmux" {
			native = append(native, command)
		}
	}
	return native
}

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
		{name: "tmux", args: []string{"-u", "has-session", "-t", "=" + projects.TargetName(destination)}},
		{name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "=" + projects.TargetName(destination) + ":", "#{session_id}"}},
		{name: "tmux", args: []string{"switch-client", "-t", "=" + projects.TargetName(destination)}},
	}
	if calls := nativeCalls(runner.calls); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if got.Container.Name != projects.TargetName(destination) {
		t.Fatalf("container = %#v, want session name", got.Container)
	}
}

func TestCreateWorktreeNoFocusSkipsSwitchClient(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	session := projects.TargetName(destination)
	runner := &fakeRunner{
		fail: map[string]error{
			"-C /repo show-ref --verify --quiet refs/heads/feature": errors.New("missing branch"),
			"has-session -t =" + session:                            errors.New("can't find session"),
		},
		outputs: map[string][]byte{
			"display-message -p -t =" + session + ": #{session_id}": []byte("$7\n"),
		},
	}
	got, err := New(runner, true).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        "/repo",
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
		NoFocus:     true,
	})
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"-u", "has-session", "-t", "=" + session}},
		{name: "tmux", args: []string{"-u", "new-session", "-d", "-s", session, "-c", destination}},
		{name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "=" + session + ":", "#{session_id}"}},
	}
	if calls := nativeCalls(runner.calls); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want no switch-client: %#v", calls, want)
	}
	if !got.Container.Created || got.Container.ID != "$7" {
		t.Fatalf("container = %#v, want created session $7", got.Container)
	}
}

func TestCreateWorktreeRollsBackWhenOpeningFails(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "feature")
	session := projects.TargetName(destination)
	wantErr := errors.New("switch failed")
	runner := &fakeRunner{fail: map[string]error{
		"-C /repo show-ref --verify --quiet refs/heads/feature": errors.New("missing branch"),
		"has-session -t =" + session:                            errors.New("can't find session"),
		"switch-client -t =" + session:                          wantErr,
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
	rollback := nativeCalls(runner.calls)
	if len(rollback) == 0 || !reflect.DeepEqual(rollback[len(rollback)-1], call{name: "tmux", args: []string{"-u", "kill-session", "-t", "=" + session}}) {
		t.Fatalf("calls = %#v, want the created session killed on rollback", rollback)
	}
}

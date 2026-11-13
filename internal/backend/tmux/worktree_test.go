package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestWorktreeUsesConfiguredName(t *testing.T) {
	for _, extract := range []bool{false, true} {
		t.Run(fmt.Sprint("extract=", extract), func(t *testing.T) {
			repo := testGitRepo(t)
			runTestGit(t, repo, "remote", "add", "origin", "git@example.test:acme/api.git")
			if extract {
				runTestGit(t, repo, "checkout", "-qb", "feature")
			}
			destination := filepath.Join(t.TempDir(), "checkout")
			r := &realGitTmuxRunner{}
			b := New(r, true)
			template := "{org}/{repo}"
			b.NameTemplate = &template
			var got capabilities.Worktree
			var err error
			if extract {
				got, err = b.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{Repo: repo, Destination: destination, NoFocus: true})
			} else {
				got, err = b.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Repo: repo, Branch: "feature", Destination: destination, NewBranch: true, NoFocus: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Container.Name != "acme/api" || !got.Container.Created {
				t.Fatalf("worktree = %#v", got)
			}
			marker, err := os.Stat(filepath.Join(destination, ".git"))
			if err != nil || marker.IsDir() {
				t.Fatalf("worktree marker = %v, %v", marker, err)
			}
			want := []call{{"tmux", []string{"-u", "has-session", "-t", "=acme/api"}}, {"tmux", []string{"-u", "new-session", "-d", "-s", "acme/api", "-c", destination}}, {"tmux", []string{"-u", "display-message", "-p", "-t", "=acme/api:", "#{session_id}"}}}
			if !reflect.DeepEqual(nativeCalls(r.calls), want) {
				t.Fatalf("native calls = %#v", nativeCalls(r.calls))
			}
		})
	}
}

func TestWorktreeNamingFailureRollsBack(t *testing.T) {
	for _, extract := range []bool{false, true} {
		t.Run(fmt.Sprint("extract=", extract), func(t *testing.T) {
			repo := testGitRepo(t)
			if extract {
				runTestGit(t, repo, "checkout", "-qb", "feature")
			}
			destination := filepath.Join(t.TempDir(), "checkout")
			r := &realGitTmuxRunner{}
			b := New(r, true)
			template := "{team}"
			b.NameTemplate = &template
			var err error
			if extract {
				_, err = b.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{Repo: repo, Destination: destination, NoFocus: true})
			} else {
				_, err = b.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Repo: repo, Branch: "feature", Destination: destination, NewBranch: true, NoFocus: true})
			}
			if err == nil || !strings.Contains(err.Error(), "name") {
				t.Fatalf("error = %v", err)
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination leaked: %v", err)
			}
			if len(nativeCalls(r.calls)) != 0 {
				t.Fatalf("native calls after naming failure = %#v", nativeCalls(r.calls))
			}
			if extract && strings.TrimSpace(runTestGitOutput(t, repo, "branch", "--show-current")) != "feature" {
				t.Fatal("original branch not restored")
			}
		})
	}
}

func TestConfiguredWorktreeRollbackKeepsAdoptedSession(t *testing.T) {
	for _, created := range []bool{false, true} {
		destination := filepath.Join(t.TempDir(), "feature")
		r := &fakeRunner{fail: map[string]error{"-C /repo show-ref --verify --quiet refs/heads/feature": errors.New("missing branch"), "switch-client -t =acme/api": errors.New("focus failed")}}
		if created {
			r.fail["has-session -t =acme/api"] = errors.New("can't find session")
		}
		r.outputFunc = func(name string, args []string) ([]byte, bool) {
			if name == "git" && commandKey(args) == "-C /repo worktree add -b feature "+destination {
				if err := os.MkdirAll(destination, 0700); err != nil {
					t.Fatal(err)
				}
			}
			return nil, false
		}
		b := New(r, true)
		template := "acme/api"
		b.NameTemplate = &template
		_, err := b.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Repo: "/repo", Branch: "feature", Destination: destination, NewBranch: true})
		if err == nil {
			t.Fatal("want focus failure")
		}
		killed := false
		removed := false
		for _, c := range r.calls {
			if c.name == "tmux" && commandKey(c.args) == "kill-session -t =acme/api" {
				killed = true
			}
			if c.name == "git" && commandKey(c.args) == "-C /repo worktree remove --force "+destination {
				removed = true
			}
		}
		if killed != created || !removed {
			t.Fatalf("created=%v, killed=%v, removed=%v; calls=%#v", created, killed, removed, r.calls)
		}
	}
}

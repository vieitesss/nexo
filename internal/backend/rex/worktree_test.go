package rex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/projects"
	"github.com/vieitesss/nexo/internal/runner"
)

// realGit serves Rex commands from fixtures and runs Git for real, so the
// worktree transaction is proven against the filesystem instead of a mock.
type realGit struct {
	rex *fakeRunner
	t   *testing.T
}

func (g realGit) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "rex" {
		g.t.Helper()
		return g.rex.Run(ctx, name, args...)
	}
	return runner.ExecRunner{}.Run(ctx, name, args...)
}

func initRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	commands := [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=nexo@example.com", "-c", "user.name=nexo", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range commands {
		if output, err := (runner.ExecRunner{}).Run(context.Background(), "git", append([]string{"-C", repo}, args...)...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repo
}

func TestCreateWorktreeAddsTheWorktreeAndOpensASession(t *testing.T) {
	repo := initRepository(t)
	destination := filepath.Join(t.TempDir(), "feature")
	target := truncateLabel(projects.TargetName(destination))
	rex := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.create", createPayload(target, destination)): createdSession,
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}}
	backend := New(realGit{rex: rex, t: t}, nil)
	backend.binary = "rex"
	backend.stateDir = t.TempDir()

	got, err := backend.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        repo,
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
	})
	if err != nil {
		t.Fatalf("CreateWorktree() error = %v", err)
	}
	if got.Path != destination || got.Branch != "feature" || !got.NewBranch {
		t.Fatalf("worktree = %#v, want the created worktree", got)
	}
	if got.ContainerID != "session:new" || !got.Container.Created || got.Container.Name != target {
		t.Fatalf("container = %#v, want the created session", got.Container)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); err != nil {
		t.Fatalf("worktree checkout missing: %v", err)
	}
}

func TestCreateWorktreeRollsBackWhenTheSessionCannotBeCreated(t *testing.T) {
	repo := initRepository(t)
	destination := filepath.Join(t.TempDir(), "feature")
	target := truncateLabel(projects.TargetName(destination))
	rex := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.create", createPayload(target, destination)): `not json`,
	}}
	backend := New(realGit{rex: rex, t: t}, nil)
	backend.binary = "rex"
	backend.stateDir = t.TempDir()

	_, err := backend.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        repo,
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
	})
	if err == nil {
		t.Fatal("CreateWorktree() error = nil, want the session creation failure")
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination still exists after rollback: %v", statErr)
	}
}

func TestExtractWorktreeMovesTheBranchAndOpensASession(t *testing.T) {
	repo := initRepository(t)
	for _, args := range [][]string{
		{"checkout", "-b", "feature"},
		{"-c", "user.email=nexo@example.com", "-c", "user.name=nexo", "commit", "--allow-empty", "-m", "work"},
	} {
		if output, err := (runner.ExecRunner{}).Run(context.Background(), "git", append([]string{"-C", repo}, args...)...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	destination := filepath.Join(t.TempDir(), "feature")
	target := truncateLabel(projects.TargetName(destination))
	rex := &fakeRunner{outputs: map[string]string{
		api("session.list", ""): sessionsJSON(),
		whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
		api("session.create", createPayload(target, destination)): createdSession,
		api("client.list", ""): oneApp,
		api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`): `{"performed_by":"client:1"}`,
	}}
	backend := New(realGit{rex: rex, t: t}, nil)
	backend.binary = "rex"
	backend.stateDir = t.TempDir()

	got, err := backend.ExtractWorktree(context.Background(), capabilities.WorktreeExtractOptions{Repo: repo, Destination: destination})
	if err != nil {
		t.Fatalf("ExtractWorktree() error = %v", err)
	}
	if got.Branch != "feature" || got.ContainerID != "session:new" {
		t.Fatalf("worktree = %#v, want the extracted feature branch in a session", got)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); err != nil {
		t.Fatalf("extracted checkout missing: %v", err)
	}
	current, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("git symbolic-ref: %v", err)
	}
	if strings.TrimSpace(string(current)) != got.ReturnBranch {
		t.Fatalf("original checkout is on %q, want the return branch %q", strings.TrimSpace(string(current)), got.ReturnBranch)
	}
}

func TestRemoveWorktreeDestroysOnlySessionsNexoCreated(t *testing.T) {
	for _, test := range []struct {
		name     string
		created  bool
		wantKill bool
	}{
		{name: "created session", created: true, wantKill: true},
		{name: "adopted session", created: false, wantKill: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "feature")
			rex := &fakeRunner{outputs: map[string]string{
				api("session.destroy", `{"session_id":"session:1"}`): `{}`,
			}}
			backend := newTestBackend(t, rex, nil)
			worktree := capabilities.Worktree{
				Path:        path,
				Repo:        "/repo",
				Branch:      "feature",
				NewBranch:   false,
				ContainerID: "session:1",
				Container:   capabilities.OpenedContainer{ID: "session:1", Created: test.created},
			}
			rex.outputs["-C /repo worktree remove --force "+path] = ""

			if err := backend.RemoveWorktree(context.Background(), worktree); err != nil {
				t.Fatalf("RemoveWorktree() error = %v", err)
			}
			destroyed := false
			for _, key := range rex.keys() {
				if strings.Contains(key, "session.destroy") {
					destroyed = true
				}
			}
			if destroyed != test.wantKill {
				t.Fatalf("commands = %#v, want kill = %v", rex.keys(), test.wantKill)
			}
		})
	}
}

func TestCreateWorktreeRollsBackTheSessionWhenFocusFailsAfterCreation(t *testing.T) {
	repo := initRepository(t)
	destination := filepath.Join(t.TempDir(), "feature")
	target := truncateLabel(projects.TargetName(destination))
	perform := `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:new"},"origin":"cli","timeout_ms":3000}`
	rex := &fakeRunner{
		outputs: map[string]string{
			api("session.list", ""): sessionsJSON(),
			whoamiKey:               `{"host_id":"host-1","instance_id":"instance-1"}`,
			api("session.create", createPayload(target, destination)): createdSession,
			api("client.list", ""):                                 oneApp,
			api("session.destroy", `{"session_id":"session:new"}`): `{}`,
		},
		errors: map[string]error{
			api("client.perform", perform): errors.New("rex client.perform: the app refused the action"),
		},
	}
	backend := New(realGit{rex: rex, t: t}, nil)
	backend.binary = "rex"
	backend.stateDir = t.TempDir()

	_, err := backend.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{
		Repo:        repo,
		Branch:      "feature",
		Destination: destination,
		NewBranch:   true,
	})
	if err == nil {
		t.Fatal("CreateWorktree() error = nil, want the refused focus")
	}
	destroyed := false
	for _, key := range rex.keys() {
		if strings.Contains(key, "session.destroy") {
			destroyed = true
		}
	}
	if !destroyed {
		t.Fatalf("commands = %#v, want the session created by this transaction destroyed", rex.keys())
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination still exists after rollback: %v", statErr)
	}
	index, err := backend.metadata(context.Background())
	if err != nil {
		t.Fatalf("metadata() error = %v", err)
	}
	if recorded, ok, err := index.lookupRoot(filepath.Clean(destination)); ok || err != nil {
		t.Fatalf("sidecar entry %q survived rollback: %v", recorded, err)
	}
}

func TestWorktreeUsesConfiguredName(t *testing.T) {
	for _, extract := range []bool{false, true} {
		t.Run(fmt.Sprint("extract=", extract), func(t *testing.T) {
			repo := initRepository(t)
			if _, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "remote", "add", "origin", "git@example.test:acme/api.git"); err != nil {
				t.Fatal(err)
			}
			if extract {
				if _, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "checkout", "-b", "feature"); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "checkout")
			r := &fakeRunner{outputs: map[string]string{api("session.list", ""): sessionsJSON(), whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`, api("session.create", createPayload("acme/api", destination)): createdSession}}
			b := New(realGit{rex: r, t: t}, nil)
			b.binary = "rex"
			b.stateDir = t.TempDir()
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
			if got.Container.Name != "acme/api" || !got.Container.Created || got.ContainerID != "session:new" {
				t.Fatalf("worktree = %#v", got)
			}
			marker, err := os.Stat(filepath.Join(destination, ".git"))
			if err != nil || marker.IsDir() {
				t.Fatalf("marker = %v, %v", marker, err)
			}
			inputs := createSessionInputs(t, r)
			if len(inputs) != 1 || inputs[0].Label != "acme/api" {
				t.Fatalf("create inputs = %#v", inputs)
			}
		})
	}
}

func TestWorktreeNamingFailureRollsBack(t *testing.T) {
	for _, extract := range []bool{false, true} {
		t.Run(fmt.Sprint("extract=", extract), func(t *testing.T) {
			repo := initRepository(t)
			if extract {
				if _, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "checkout", "-b", "feature"); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(t.TempDir(), "checkout")
			r := &fakeRunner{outputs: map[string]string{api("session.list", ""): sessionsJSON(), whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`}}
			b := New(realGit{rex: r, t: t}, nil)
			b.binary = "rex"
			b.stateDir = t.TempDir()
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
			for _, key := range r.keys() {
				if strings.Contains(key, "session.create") || strings.Contains(key, "session.destroy") {
					t.Fatalf("unexpected mutation: %s", key)
				}
			}
			if extract {
				current, err := (runner.ExecRunner{}).Run(context.Background(), "git", "-C", repo, "branch", "--show-current")
				if err != nil || strings.TrimSpace(string(current)) != "feature" {
					t.Fatalf("original branch = %s, %v", current, err)
				}
			}
		})
	}
}

func TestConfiguredWorktreeAdoptionPreservesNativeLabel(t *testing.T) {
	for _, failFocus := range []bool{false, true} {
		t.Run(fmt.Sprint("focus failure=", failFocus), func(t *testing.T) {
			repo := initRepository(t)
			destination := filepath.Join(t.TempDir(), "feature")
			fixtures := map[string]string{api("session.list", ""): sessionsJSON(sessionJSON("session:1", "native/name")), whoamiKey: `{"host_id":"host-1","instance_id":"instance-1"}`, api("session.list_blocks", `{"session_id":"session:1"}`): blocksJSON(terminalBlockJSON("block:1", "host-1"))}
			addBlockPaths(fixtures, "session:1", "block:1", destination)
			r := &fakeRunner{outputs: fixtures}
			focusErr := errors.New("focus refused")
			if failFocus {
				fixtures[api("client.list", "")] = oneApp
				r.errors = map[string]error{api("client.perform", `{"client_id":"client:1","action":"session.select","args":{"session_id":"session:1"},"origin":"cli","timeout_ms":3000}`): focusErr}
			}
			b := New(realGit{rex: r, t: t}, nil)
			b.binary = "rex"
			b.stateDir = t.TempDir()
			template := "{team}"
			b.NameTemplate = &template
			got, err := b.CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Repo: repo, Branch: "feature", Destination: destination, NewBranch: true, NoFocus: !failFocus})
			if failFocus {
				if !errors.Is(err, focusErr) {
					t.Fatalf("error=%v", err)
				}
				if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("destination leaked: %v", err)
				}
			} else {
				if err != nil || got.Container.Created || got.Container.Name != "native/name" {
					t.Fatalf("result=%#v, %v", got, err)
				}
				if err := b.RemoveWorktree(context.Background(), got); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range r.keys() {
				if strings.Contains(key, "session.create") || strings.Contains(key, "session.destroy") {
					t.Fatalf("mutated adopted session: %s", key)
				}
			}
		})
	}
}

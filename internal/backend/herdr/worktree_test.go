package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// namingWorktreeRunner models native creation before nexo fills a missing result label.
type namingWorktreeRunner struct {
	fake        *fakeRunner
	destination string
	cancel      context.CancelFunc
}

func (r namingWorktreeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "herdr" && len(args) > 1 && args[0] == "worktree" && args[1] == "create" {
		if err := os.MkdirAll(r.destination, 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(r.destination, ".git"), []byte("gitdir: /repo/.git/worktrees/feature\n"), 0600); err != nil {
			return nil, err
		}
		if r.cancel != nil {
			r.cancel()
		}
	}
	output, err := r.fake.Run(ctx, name, args...)
	if err == nil && name == "herdr" && len(args) > 1 && args[0] == "worktree" && args[1] == "remove" {
		err = os.RemoveAll(r.destination)
	}
	return output, err
}

func TestWorktreeUsesConfiguredName(t *testing.T) {
	for _, tc := range []struct {
		name, label, template, want string
		cancelled, cleanupFails     bool
	}{
		{name: "native authority", label: "native-feature", template: "{team}", want: "native-feature"},
		{name: "empty native label", template: "{org}/{repo}", want: "acme/api"},
		{name: "render failure", template: "{team}"},
		{name: "caller cancellation", template: "{org}/{repo}", cancelled: true},
		{name: "cleanup errors joined", template: "{team}", cleanupFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "checkout")
			create := []string{"worktree", "create", "--cwd", "/repo", "--branch", "feature", "--path", destination}
			f := &fakeRunner{outputs: map[string][]byte{
				strings.Join(create, " "):                               []byte(`{"workspace":{"workspace_id":"created","label":"` + tc.label + `"}}`),
				"-C " + destination + " rev-parse --show-toplevel":      []byte(destination),
				"-C " + destination + " remote":                         []byte("origin\n"),
				"-C " + destination + " remote get-url --all -- origin": []byte("https://user:synthetic-secret@example.test/acme/api.git"),
			}}
			cleanupErr := errors.New("native cleanup failed")
			gitErr := errors.New("git cleanup failed")
			if tc.cleanupFails {
				f.errors = map[string]error{"worktree remove --workspace created --force": cleanupErr, "-C /repo worktree remove --force " + destination: gitErr}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := namingWorktreeRunner{fake: f, destination: destination}
			if tc.cancelled {
				r.cancel = cancel
			}
			b := New(r)
			b.NameTemplate = &tc.template
			got, err := b.CreateWorktree(ctx, capabilities.WorktreeCreateOptions{Repo: "/repo", Branch: "feature", Destination: destination, NoFocus: true})
			if tc.want != "" {
				if err != nil || got.Container.Name != tc.want || !got.Container.Created || got.Container.ID != "created" || got.SessionName != tc.want {
					t.Fatalf("result = %#v, %v", got, err)
				}
				want := []call{{"herdr", create}}
				if tc.label == "" {
					want = append(want, call{"git", []string{"-C", destination, "rev-parse", "--show-toplevel"}}, call{"git", []string{"-C", destination, "remote"}}, call{"git", []string{"-C", destination, "remote", "get-url", "--all", "--", "origin"}})
				}
				if !reflect.DeepEqual(f.calls, want) {
					t.Fatalf("calls = %#v, want %#v", f.calls, want)
				}
				return
			}
			if err == nil {
				t.Fatal("want naming failure after native creation")
			}
			if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "https://") {
				t.Fatalf("credentials leaked: %v", err)
			}
			if tc.cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
			if tc.cleanupFails {
				if !errors.Is(err, cleanupErr) || !errors.Is(err, gitErr) {
					t.Fatalf("cleanup errors lost: %v", err)
				}
			} else if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination leaked: %v", err)
			}
			found := false
			for _, c := range f.calls {
				if c.name == "herdr" && reflect.DeepEqual(c.args, []string{"worktree", "remove", "--workspace", "created", "--force"}) {
					found = true
				}
			}
			if !found {
				t.Fatalf("rollback missing: %#v", f.calls)
			}
		})
	}
}

func TestWorktreeWithoutTemplateKeepsLegacyEmptyLabelDefault(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "acme", "feature")
	create := []string{"worktree", "create", "--cwd", "/repo", "--branch", "feature", "--path", destination}
	r := &fakeRunner{outputs: map[string][]byte{
		strings.Join(create, " "): []byte(`{"workspace":{"workspace_id":"created","label":""}}`),
	}}
	got, err := New(r).CreateWorktree(context.Background(), capabilities.WorktreeCreateOptions{Repo: "/repo", Branch: "feature", Destination: destination, NoFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Container.Name != "acme_feature" || got.SessionName != "" || !got.Container.Created {
		t.Fatalf("legacy result = %#v", got)
	}
	if !reflect.DeepEqual(r.calls, []call{{"herdr", create}}) {
		t.Fatalf("calls = %#v", r.calls)
	}
}

package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
)

type fakeWorktreeCreator struct {
	options     capabilities.WorktreeCreateOptions
	result      capabilities.Worktree
	err         error
	removed     bool
	removeError error
}

func (f *fakeWorktreeCreator) CreateWorktree(_ context.Context, options capabilities.WorktreeCreateOptions) (capabilities.Worktree, error) {
	f.options = options
	if f.err != nil {
		return capabilities.Worktree{}, f.err
	}
	return f.result, nil
}

func (f *fakeWorktreeCreator) RemoveWorktree(context.Context, capabilities.Worktree) error {
	f.removed = true
	return f.removeError
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestWorktreeCreateCommandPrintsJSON(t *testing.T) {
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/created", Branch: "feature"}}
	command, ok := NewRegistry().Lookup("worktree")
	if !ok {
		t.Fatal("worktree command is not registered")
	}
	var output strings.Builder
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   creator,
		Args:      []string{"create", "--new-branch", "/repo", "feature", "/tmp/created"},
		Stdout:    &output,
		JSON:      true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), `"path":"/tmp/created"`) || !strings.Contains(output.String(), `"branch":"feature"`) {
		t.Fatalf("output = %q", output.String())
	}
	if !creator.options.NewBranch || creator.options.Repo != "/repo" {
		t.Fatalf("options = %#v", creator.options)
	}
}

func TestWorktreeCreateCommandAddsParentAndRestoresOnFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := "# retain\npaths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("create failed")
	creator := &fakeWorktreeCreator{err: wantErr}
	command, _ := NewRegistry().Lookup("worktree")
	destination := filepath.Join(filepath.Dir(configPath), "group", "created")
	err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		ConfigPath: configPath,
		Adapter:    creator,
		Args:       []string{"create", "--add-parent", "/repo", "feature", destination},
		Stdout:     &bytes.Buffer{},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil || string(got) != original {
		t.Fatalf("config after failure = %q, %v", got, readErr)
	}
}

func TestWorktreeCreateCommandRollsBackOnOutputFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := "paths = [\"/existing\"]\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/created", Branch: "feature"}}
	command, _ := NewRegistry().Lookup("worktree")
	err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		ConfigPath: configPath,
		Adapter:    creator,
		Args:       []string{"create", "--add-parent", "/repo", "feature", filepath.Join(filepath.Dir(configPath), "group", "created")},
		Stdout:     failingWriter{},
		JSON:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "output failed") {
		t.Fatalf("Run() error = %v, want output failure", err)
	}
	if !creator.removed {
		t.Fatal("creator was not asked to roll back")
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil || string(got) != original {
		t.Fatalf("config after output failure = %q, %v", got, readErr)
	}
}

func TestWorktreeCreateCommandAcceptsDocumentedInvocation(t *testing.T) {
	creator := &fakeWorktreeCreator{result: capabilities.Worktree{Path: "/tmp/created", Branch: "feature"}}
	command, _ := NewRegistry().Lookup("worktree")
	var output bytes.Buffer
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   creator,
		Args:      []string{"create", "/repo", "feature", "/tmp/created"},
		Stdout:    &output,
	}); err != nil {
		t.Fatalf("documented invocation error = %v", err)
	}
	if !strings.Contains(output.String(), "created worktree") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestParseWorktreeCreateArgs(t *testing.T) {
	parsed, err := parseWorktreeCreateArgs([]string{"create", "--add-parent", "/repo", "branch", "/tmp/destination", "--new-branch"})
	if err != nil {
		t.Fatalf("parseWorktreeCreateArgs() error = %v", err)
	}
	if !parsed.AddParent || !parsed.NewBranch || parsed.Repo != "/repo" || parsed.Branch != "branch" || parsed.Destination != "/tmp/destination" {
		t.Fatalf("parsed = %#v", parsed)
	}
	for _, args := range [][]string{{"create", "/repo", "branch"}, {"create", "--unknown", "/repo", "branch", "/tmp/destination"}} {
		if _, err := parseWorktreeCreateArgs(args); err == nil {
			t.Fatalf("parseWorktreeCreateArgs(%v) error = nil", args)
		}
	}
}

func TestWorktreeCreateCommandRequiresCreator(t *testing.T) {
	command, _ := NewRegistry().Lookup("worktree")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Args:      []string{"create", "/repo", "branch", "/tmp/destination"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support creating worktrees") {
		t.Fatalf("error = %v", err)
	}
}

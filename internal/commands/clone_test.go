package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

type cloneCall struct {
	name string
	args []string
}

type cloneRunner struct {
	calls []cloneCall
	err   error
}

func (r *cloneRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, cloneCall{name: name, args: append([]string(nil), args...)})
	return nil, r.err
}

func TestCloneCommandPassesCompleteSourceToGit(t *testing.T) {
	runner := &cloneRunner{}
	command, ok := NewRegistry().Lookup("clone")
	if !ok {
		t.Fatal("clone command is not registered")
	}
	var output bytes.Buffer
	if err := command.Run(context.Background(), Invocation{Runner: runner, Stdout: &output, Args: []string{"git@github.com:owner/repo.git", "/tmp/new-repo"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := []cloneCall{{name: "git", args: []string{"clone", "--", "git@github.com:owner/repo.git", "/tmp/new-repo"}}}; !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Git calls = %#v, want %#v", runner.calls, want)
	}
	if !strings.Contains(output.String(), "cloned git@github.com:owner/repo.git") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestCloneCommandResolvesOmittedDestination(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	runner := &cloneRunner{}
	command, _ := NewRegistry().Lookup("clone")
	var output bytes.Buffer
	if err := command.Run(context.Background(), Invocation{Runner: runner, Stdout: &output, JSON: true, Args: []string{"https://example.test/team/repo.git"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := filepath.Join(root, "repo")
	if !strings.Contains(output.String(), `"destination":"`+want+`"`) {
		t.Fatalf("JSON = %q, want resolved destination %q", output.String(), want)
	}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, []string{"clone", "--", "https://example.test/team/repo.git", want}) {
		t.Fatalf("Git calls = %#v", runner.calls)
	}
}

func TestCloneCommandRejectsExistingOmittedDestination(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	runner := &cloneRunner{}
	command, _ := NewRegistry().Lookup("clone")
	err := command.Run(context.Background(), Invocation{Runner: runner, Args: []string{"https://example.test/team/repo.git"}})
	if err == nil || !strings.Contains(err.Error(), "already exists") || len(runner.calls) != 0 {
		t.Fatalf("Run() = %v, calls = %#v; want existing destination refusal", err, runner.calls)
	}
}

func TestCloneCommandWritesJSONAndAvoidsGitForInvalidDestination(t *testing.T) {
	root := t.TempDir()
	runner := &cloneRunner{}
	command, _ := NewRegistry().Lookup("clone")
	var output bytes.Buffer
	err := command.Run(context.Background(), Invocation{
		Runner: runner, Stdout: &output, JSON: true,
		Config: config.Config{}, Args: []string{"https://example/repo", filepath.Join(root, "missing", "repo")},
	})
	if err == nil || !strings.Contains(err.Error(), "parent") || len(runner.calls) != 0 {
		t.Fatalf("Run() = %v, calls = %#v; want preflight refusal", err, runner.calls)
	}
}

func TestCloneCommandAddsParentOnSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := filepath.Join(home, "projects")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("paths = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &cloneRunner{}
	command, _ := NewRegistry().Lookup("clone")
	if err := command.Run(context.Background(), Invocation{
		Runner: runner, ConfigPath: configPath, Config: config.Config{}, Stdout: &bytes.Buffer{},
		Args: []string{"--add-parent", "owner/repo", filepath.Join(parent, "repo")},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(contents), parent) {
		t.Fatalf("config = %q, %v; want added parent", contents, err)
	}
}

func TestCloneCommandAddsParentBeforeGitAndRestoresOnFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	parent := filepath.Join(home, "projects")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "config.toml")
	original := "paths = []\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("git failed")
	runner := &cloneRunner{err: wantErr}
	command, _ := NewRegistry().Lookup("clone")
	err := command.Run(context.Background(), Invocation{
		Runner: runner, ConfigPath: configPath, Config: config.Config{ProjectsConfig: config.ProjectsConfig{}},
		Args: []string{"--add-parent", "owner/repo", filepath.Join(parent, "repo")},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	got, readErr := os.ReadFile(configPath)
	if readErr != nil || string(got) != original {
		t.Fatalf("config after failed clone = %q, %v; want restoration", got, readErr)
	}
}

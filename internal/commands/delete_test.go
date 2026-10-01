package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestDeleteCommandDryRunPrintsPreflightAndDoesNothing(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "repo")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &deleteRunner{err: errors.New("not a git repository")}
	var out bytes.Buffer
	command, ok := NewRegistry().Lookup("delete")
	if !ok {
		t.Fatal("delete command is not registered")
	}
	err := command.Run(context.Background(), Invocation{Runner: runner, Stdout: &out, Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}}, Args: []string{"--dry-run", candidate}})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !strings.Contains(out.String(), "preflight") || !strings.Contains(out.String(), "non-empty") {
		t.Fatalf("output = %q", out.String())
	}
	if _, err := os.Lstat(candidate); err != nil {
		t.Fatalf("dry run removed candidate: %v", err)
	}
}

func TestDeleteCommandPermanentRequiresForceForWarnings(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "repo")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "file"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &deleteRunner{err: errors.New("not a git repository")}
	command, _ := NewRegistry().Lookup("delete")
	err := command.Run(context.Background(), Invocation{Runner: runner, Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}}, Args: []string{"--permanent", candidate}})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("Run() = %v, want warning refusal", err)
	}
	if _, err := os.Lstat(candidate); err != nil {
		t.Fatalf("warning refusal removed candidate: %v", err)
	}
	if err := command.Run(context.Background(), Invocation{Runner: runner, Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}}, Args: []string{"--permanent", "--force", candidate}}); err != nil {
		t.Fatalf("forced Run() = %v", err)
	}
	if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
		t.Fatalf("candidate remains: %v", err)
	}
}

type deleteRunner struct{ err error }

func (r *deleteRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, r.err }

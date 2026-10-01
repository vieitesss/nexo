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
	"github.com/vieitesss/nexo/internal/deletion"
)

func TestDeleteCommandDryRunPrintsPreflightAndDoesNothing(t *testing.T) {
	setDeleteTestEnv(t)
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
	err := command.Run(context.Background(), Invocation{Runner: runner, Adapter: deleteUseChecker{}, Stdout: &out, Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}}, Args: []string{"--dry-run", candidate}})
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
	setDeleteTestEnv(t)
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
	err := command.Run(context.Background(), Invocation{Runner: runner, Adapter: deleteUseChecker{}, Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}}, Args: []string{"--permanent", candidate}})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("Run() = %v, want warning refusal", err)
	}
	if _, err := os.Lstat(candidate); err != nil {
		t.Fatalf("warning refusal removed candidate: %v", err)
	}
	if err := command.Run(context.Background(), Invocation{Runner: runner, Adapter: deleteUseChecker{}, Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}}, Args: []string{"--permanent", "--force", candidate}}); err != nil {
		t.Fatalf("forced Run() = %v", err)
	}
	if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
		t.Fatalf("candidate remains: %v", err)
	}
}

type deleteRunner struct{ err error }

func (r *deleteRunner) Run(context.Context, string, ...string) ([]byte, error) { return nil, r.err }

type deleteUseChecker struct{}

func (deleteUseChecker) ProjectActive(context.Context, string) (bool, error) { return false, nil }

func setDeleteTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
}

func TestDeletePreservesExactTrailingSpaceAndNewlineNames(t *testing.T) {
	setDeleteTestEnv(t)
	root := t.TempDir()
	spaced := filepath.Join(root, "item ")
	newline := filepath.Join(root, "item\n")
	plain := filepath.Join(root, "item")
	for _, path := range []string{spaced, newline, plain} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	command, _ := NewRegistry().Lookup("delete")
	if err := command.Run(context.Background(), Invocation{
		Runner:  &deleteRunner{err: errors.New("not a git repository")},
		Adapter: deleteUseChecker{},
		Config:  config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Args:    []string{"--permanent", "--force", spaced},
	}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if _, err := os.Lstat(spaced); !os.IsNotExist(err) {
		t.Fatalf("selected path remains: %v", err)
	}
	for _, path := range []string{newline, plain} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("sibling %q changed: %v", path, err)
		}
	}
}

func TestDeleteRejectsReplacementAfterSelectionEvenWithForce(t *testing.T) {
	setDeleteTestEnv(t)
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	if err := os.MkdirAll(filepath.Join(candidate, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	identity, err := deletion.Fingerprint(deletion.OSFileSystem{}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := candidate + ".old"
	if err := os.Rename(candidate, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(candidate, "replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	command, _ := NewRegistry().Lookup("delete")
	err = command.Run(context.Background(), Invocation{
		Runner:         &deleteRunner{err: errors.New("not a git repository")},
		Adapter:        deleteUseChecker{},
		DeleteIdentity: identity,
		Config:         config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Args:           []string{"--permanent", "--force", candidate},
	})
	if err == nil || !strings.Contains(err.Error(), "changed after preflight") {
		t.Fatalf("Run() = %v, want replacement blocker", err)
	}
	if _, err := os.Lstat(filepath.Join(candidate, "replacement")); err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
}

func TestDeleteExpandsLiteralHomeOperand(t *testing.T) {
	setDeleteTestEnv(t)
	home := os.Getenv("HOME")
	candidate := filepath.Join(home, "repo")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	command, _ := NewRegistry().Lookup("delete")
	var out bytes.Buffer
	if err := command.Run(context.Background(), Invocation{
		Runner:  &deleteRunner{err: errors.New("not a git repository")},
		Adapter: deleteUseChecker{},
		Stdout:  &out,
		Config:  config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{home}}},
		Args:    []string{"--dry-run", "~/repo"},
	}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !strings.Contains(out.String(), candidate) || strings.Contains(out.String(), string(filepath.Join(".", "~", "repo"))) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDeleteFailsClosedWithoutActiveUseCapability(t *testing.T) {
	setDeleteTestEnv(t)
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	command, _ := NewRegistry().Lookup("delete")
	err := command.Run(context.Background(), Invocation{
		Runner: &deleteRunner{err: errors.New("not a git repository")},
		Config: config.Config{ProjectsConfig: config.ProjectsConfig{Paths: []string{root}}},
		Args:   []string{"--dry-run", candidate},
	})
	if err == nil || !strings.Contains(err.Error(), "active project use") {
		t.Fatalf("Run() = %v, want unavailable safety capability blocker", err)
	}
}

func TestDeleteProtectsHomeAndConfiguredParents(t *testing.T) {
	setDeleteTestEnv(t)
	home := os.Getenv("HOME")
	root := t.TempDir()
	parent := filepath.Join(root, "group")
	if err := os.MkdirAll(filepath.Join(parent, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	command, _ := NewRegistry().Lookup("delete")
	invoke := func(path string, paths []string) error {
		return command.Run(context.Background(), Invocation{
			Runner:  &deleteRunner{err: errors.New("not a git repository")},
			Adapter: deleteUseChecker{},
			Config:  config.Config{ProjectsConfig: config.ProjectsConfig{Paths: paths}},
			Args:    []string{"--permanent", "--force", path},
		})
	}
	if err := invoke(home, []string{root}); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("home deletion error = %v", err)
	}
	if err := invoke(parent, []string{root, parent}); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("configured parent deletion error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "child")); err != nil {
		t.Fatalf("protected child changed: %v", err)
	}
}

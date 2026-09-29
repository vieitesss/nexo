package projects

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestDiscoverExpandsConfiguredPathsAndHomeRepositories(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "work")
	mustMkdir(t, filepath.Join(root, "alpha", ".git"))
	mustMkdir(t, filepath.Join(root, "beta", "one"))
	mustWrite(t, filepath.Join(root, "beta", "one", ".git"), "gitdir: ../.git/worktrees/one\n")
	mustMkdir(t, filepath.Join(root, "beta", "two"))

	mustMkdir(t, filepath.Join(home, "home-repo", ".git"))
	mustMkdir(t, filepath.Join(home, "home-dir"))

	got := Discover(config.ProjectsConfig{
		Paths:       []string{root, root + string(filepath.Separator), root + string(filepath.Separator) + "*"},
		GitFromHome: true,
	}, home)
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", got.Warnings)
	}
	want := []Candidate{
		{Path: filepath.Join(root, "alpha"), Group: root, Git: true},
		{Path: filepath.Join(root, "beta"), Group: root},
		{Path: filepath.Join(root, "beta", "one"), Group: filepath.Join(root, "beta"), Git: true, LinkedWorktree: true},
		{Path: filepath.Join(root, "beta", "two"), Group: filepath.Join(root, "beta")},
		{Path: filepath.Join(home, "home-repo"), Group: home, Git: true},
	}
	if !reflect.DeepEqual(got.Candidates, want) {
		t.Fatalf("candidates = %#v, want %#v", got.Candidates, want)
	}
}

func TestDiscoverExpandsHomePrefixes(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "work")
	mustMkdir(t, filepath.Join(root, "project"))

	got := Discover(config.ProjectsConfig{Paths: []string{"~/work", "$HOME/work/"}}, home)
	if len(got.Candidates) != 1 || got.Candidates[0].Path != filepath.Join(root, "project") {
		t.Fatalf("candidates = %#v, want expanded home paths", got.Candidates)
	}
}

func TestDiscoverCanonicalizesAndDeduplicatesCandidates(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "projects")
	candidate := filepath.Join(root, "one")
	mustMkdir(t, candidate)
	link := filepath.Join(home, "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := Discover(config.ProjectsConfig{Paths: []string{root, link}}, home)
	if len(got.Candidates) != 1 || got.Candidates[0].Path != candidate {
		t.Fatalf("candidates = %#v, want one canonical candidate", got.Candidates)
	}
}

func TestDiscoverWarnsAndSkipsMissingOrUnreadablePaths(t *testing.T) {
	home := t.TempDir()
	unreadable := filepath.Join(home, "not-a-directory")
	mustWrite(t, unreadable, "file")
	got := Discover(config.ProjectsConfig{Paths: []string{filepath.Join(home, "missing"), unreadable}}, home)
	if len(got.Candidates) != 0 {
		t.Fatalf("candidates = %#v, want none", got.Candidates)
	}
	if len(got.Warnings) != 2 || got.Warnings[0] == "" || got.Warnings[1] == "" {
		t.Fatalf("warnings = %#v, want warnings for both paths", got.Warnings)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

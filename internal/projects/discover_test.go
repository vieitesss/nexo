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

func TestDiscoverExpansionForms(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "configured")
	mustMkdir(t, filepath.Join(home, "home-project"))
	mustMkdir(t, filepath.Join(dir, "direct"))
	mustMkdir(t, filepath.Join(dir, "nested", "project"))

	tests := []struct {
		name    string
		pattern string
		want    []Candidate
	}{
		{name: "tilde", pattern: "~", want: []Candidate{{Path: filepath.Join(home, "configured"), Group: home}, {Path: filepath.Join(home, "home-project"), Group: home}}},
		{name: "tilde slash", pattern: "~/", want: []Candidate{{Path: filepath.Join(home, "configured"), Group: home}, {Path: filepath.Join(home, "home-project"), Group: home}}},
		{name: "home variable", pattern: "$HOME", want: []Candidate{{Path: filepath.Join(home, "configured"), Group: home}, {Path: filepath.Join(home, "home-project"), Group: home}}},
		{name: "home variable slash", pattern: "$HOME/", want: []Candidate{{Path: filepath.Join(home, "configured"), Group: home}, {Path: filepath.Join(home, "home-project"), Group: home}}},
		{name: "directory", pattern: dir, want: []Candidate{{Path: filepath.Join(dir, "direct"), Group: dir}, {Path: filepath.Join(dir, "nested"), Group: dir}}},
		{name: "directory slash", pattern: dir + string(filepath.Separator), want: []Candidate{{Path: filepath.Join(dir, "direct"), Group: dir}, {Path: filepath.Join(dir, "nested"), Group: dir}}},
		{name: "directory wildcard", pattern: dir + string(filepath.Separator) + "*", want: []Candidate{{Path: filepath.Join(dir, "nested", "project"), Group: filepath.Join(dir, "nested")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Discover(config.ProjectsConfig{Paths: []string{test.pattern}}, home)
			if len(got.Warnings) != 0 {
				t.Fatalf("warnings = %v, want none", got.Warnings)
			}
			if !reflect.DeepEqual(got.Candidates, test.want) {
				t.Fatalf("candidates = %#v, want %#v", got.Candidates, test.want)
			}
		})
	}
}

func TestDiscoverEmptyHomeSkipsHomeExpansionsAndGitDiscovery(t *testing.T) {
	tests := []string{"~", "~/", "$HOME", "$HOME/", "$HOME/work", "$HOME/work/*"}
	got := Discover(config.ProjectsConfig{Paths: tests, GitFromHome: true}, "")
	if len(got.Candidates) != 0 {
		t.Fatalf("candidates = %#v, want none", got.Candidates)
	}
	if len(got.Warnings) != len(tests)+1 {
		t.Fatalf("warnings = %#v, want one per home operation", got.Warnings)
	}
}

func TestCanonicalRejectsEmptyPath(t *testing.T) {
	if _, err := canonical(""); err == nil {
		t.Fatal("canonical(\"\") error = nil, want rejection")
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

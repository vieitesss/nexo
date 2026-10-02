package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddProjectParentUsesDestinationParent(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	destination := filepath.Join(root, "projects", "repo")
	if _, _, err := AddProjectParent(configPath, destination, root); err != nil {
		t.Fatalf("AddProjectParent() error = %v", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil || string(contents) != "paths = [\""+filepath.Join(root, "projects")+"\"]\n" {
		t.Fatalf("config = %q, %v", contents, err)
	}
}

func TestAddProjectPathPreservesCommentsAndFormatting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "# keep this\nbackend = \"tmux\"\npaths = [\n  \"~/work\", # first\n]\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	restore, changed, err := AddProjectPath(path, "/home/tester/projects", "/home/tester")
	if err != nil || !changed {
		t.Fatalf("AddProjectPath() = (%v, %v), want changed", err, changed)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# keep this\nbackend = \"tmux\"\npaths = [\n  \"~/work\", # first\n  \"/home/tester/projects\",\n]\n"
	if string(got) != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode = %v, %v; want 0640", info.Mode().Perm(), err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != original {
		t.Fatalf("restore() config = %q, %v", got, err)
	}
}

func TestAddProjectPathRestorePreservesConcurrentEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "paths = [\"/existing\"]\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, changed, err := AddProjectPath(path, "/projects", "")
	if err != nil || !changed {
		t.Fatalf("AddProjectPath() = (%v, %v), want changed", err, changed)
	}
	if err := os.WriteFile(path, []byte("paths = [\"/other\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restore(); err == nil {
		t.Fatal("restore() error = nil, want concurrent edit refusal")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "paths = [\"/other\"]\n" {
		t.Fatalf("config after refused restore = %q, %v", got, err)
	}
}

func TestAddProjectPathHandlesLiteralAndMultilineStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "backend = '''\npaths = [\"not-a-key\"] # still a string\n# also text\n'''\npaths = [\"~/work\"] # preserve this comment\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := AddProjectPath(path, "/home/tester/projects", "/home/tester"); err != nil || !changed {
		t.Fatalf("AddProjectPath() = (%v, %v), want changed", err, changed)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "backend = '''\npaths = [\"not-a-key\"] # still a string\n# also text\n'''\npaths = [\"~/work\", \"/home/tester/projects\"] # preserve this comment\n"
	if string(got) != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
}

func TestAddProjectPathDoesNotDuplicateCoveredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "paths = [\"~/work/*\"]\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, changed, err := AddProjectPath(path, "/home/tester/work", "/home/tester")
	if err != nil || changed || restore == nil {
		t.Fatalf("AddProjectPath() = (%v, %v), want unchanged", err, changed)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("config = %q, want unchanged", got)
	}
}

func TestAddProjectPathCreatesMinimalFileAndRestoresIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	restore, changed, err := AddProjectPath(path, "/tmp/work", "")
	if err != nil || !changed {
		t.Fatalf("AddProjectPath() = (%v, %v), want changed", err, changed)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "paths = [\"/tmp/work\"]\n" {
		t.Fatalf("created config = %q, %v", got, err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("restored config stat error = %v, want not exist", err)
	}
}

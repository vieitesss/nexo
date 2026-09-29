package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != original {
		t.Fatalf("restore() config = %q, %v", got, err)
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

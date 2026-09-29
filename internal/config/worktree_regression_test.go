package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddProjectPathHandlesEscapedTripleQuoteInMultilineString(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "backend = \"\"\"\nescaped \\\"\"\"\nstill backend\n\"\"\"\npaths = [\"~/work\"]\n"
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
	want := "backend = \"\"\"\nescaped \\\"\"\"\nstill backend\n\"\"\"\npaths = [\"~/work\", \"/home/tester/projects\"]\n"
	if string(got) != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
}

func TestAddProjectPathRecognizesQuotedTopLevelPathsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "\"paths\" = [\"~/work\"]\n"
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
	want := "\"paths\" = [\"~/work\", \"/home/tester/projects\"]\n"
	if string(got) != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
}

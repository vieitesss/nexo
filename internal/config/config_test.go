package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadParsesBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("backend = \"tmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Backend != "tmux" {
		t.Fatalf("Backend = %q, want tmux", got.Backend)
	}
}

func TestLoadUnknownKeyNamesFileAndKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("ui = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want unknown-key error")
	}
	message := err.Error()
	if !strings.Contains(message, path) || !strings.Contains(message, "ui") {
		t.Fatalf("error = %q, want file and key", message)
	}
}

func TestLoadOptionalMissingIsEmpty(t *testing.T) {
	got, err := LoadOptional(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadOptional() error = %v", err)
	}
	if got != (Config{}) {
		t.Fatalf("config = %#v, want empty config", got)
	}
}

func TestDefaultPathHonorsXDG(t *testing.T) {
	if got, want := DefaultPathFrom("/home/tester", "/tmp/config"), "/tmp/config/nexo/config.toml"; got != want {
		t.Fatalf("DefaultPathFrom() = %q, want %q", got, want)
	}
	if got, want := DefaultPathFrom("/home/tester", ""), "/home/tester/.config/nexo/config.toml"; got != want {
		t.Fatalf("DefaultPathFrom() = %q, want %q", got, want)
	}
}

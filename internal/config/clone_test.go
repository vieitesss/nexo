package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadParsesCloneDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[clone]\ndefault-protocol = \"https\"\nssh-prefix = \"ssh://git@example/\"\nhttps-prefix = \"https://example/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Clone.DefaultProtocol != "https" || got.Clone.SSHPrefix != "ssh://git@example/" || got.Clone.HTTPSPrefix != "https://example/" {
		t.Fatalf("Clone = %#v, want configured values", got.Clone)
	}
}

func TestLoadDefaultsCloneConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := (CloneConfig{}).WithDefaults()
	if got.Clone != want {
		t.Fatalf("Clone = %#v, want %#v", got.Clone, want)
	}
}

func TestLoadRejectsInvalidCloneProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[clone]\ndefault-protocol = \"file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want clone protocol validation")
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSessionNameTemplate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
		want     *string
	}{
		{"absent", "backend = \"tmux\"\n", nil},
		{"empty table", "[session]\n", nil},
		{"configured", "[session]\nname-template = \"{org}/{team}/{repo}\"\n", new("{org}/{team}/{repo}")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if tc.want == nil {
				if got.Session.NameTemplate != nil {
					t.Fatalf("NameTemplate = %q; want nil", *got.Session.NameTemplate)
				}
			} else if got.Session.NameTemplate == nil || *got.Session.NameTemplate != *tc.want {
				t.Fatalf("NameTemplate = %v; want %q", got.Session.NameTemplate, *tc.want)
			}
			if got.Clone.DefaultProtocol != CloneProtocolSSH || got.TUI.Theme != ThemeAuto {
				t.Fatalf("legacy defaults changed: Clone = %#v, TUI = %#v", got.Clone, got.TUI)
			}
		})
	}
}

func TestLoadRejectsInvalidSessionNameTemplate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
		want     string
		key      string
	}{
		{"empty", "name-template = \"\"", "empty", "session.name-template"},
		{"whitespace", "name-template = \"  \"", "empty", "session.name-template"},
		{"unknown placeholder", "name-template = \"{branch}\"", "placeholder", "session.name-template"},
		{"unmatched brace", "name-template = \"{repo\"", "brace", "session.name-template"},
		{"unknown nested key", "other = true", "unknown key", "session.other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[session]\n"+tc.contents+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
				t.Fatalf("Load() error = %v; want path, full key %q and %q", err, tc.key, tc.want)
			}
		})
	}
}

func TestPrepareSetupPreservesConfigWithoutSessionTable(t *testing.T) {
	contents := []byte("backend = \"tmux\"\n# keep existing formatting\n[clone]\ndefault-protocol = \"https\"\n")
	got, err := PrepareSetup(contents, "config.toml", "/home/test", SetupValues{})
	if err != nil || string(got) != string(contents) {
		t.Fatalf("PrepareSetup() = %q, %v; want original bytes", got, err)
	}
}

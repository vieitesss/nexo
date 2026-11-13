package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTUIConfig(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadParsesProjectActions(t *testing.T) {
	cfg, err := loadTUIConfig(t, `
[[tui.project-actions]]
key = "ctrl+s"
behavior = "split-right"
label = "split"

[[tui.project-actions]]
key = "alt+g"
behavior = "float"
`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	actions := cfg.TUI.ProjectActions
	if len(actions) != 2 {
		t.Fatalf("project actions = %#v, want 2", actions)
	}
	if actions[0].DisplayKey() != "Ctrl-S" || actions[0].DisplayLabel() != "split" {
		t.Fatalf("first action renders %q %q", actions[0].DisplayKey(), actions[0].DisplayLabel())
	}
	if actions[1].DisplayKey() != "Alt-G" || actions[1].DisplayLabel() != "float" {
		t.Fatalf("second action renders %q %q, want label defaulting to behavior", actions[1].DisplayKey(), actions[1].DisplayLabel())
	}
}

func TestLoadRejectsInvalidProjectActions(t *testing.T) {
	action := func(key, behavior, label string) string {
		return "[[tui.project-actions]]\nkey = \"" + key + "\"\nbehavior = \"" + behavior + "\"\nlabel = \"" + label + "\"\n"
	}
	tests := map[string]struct {
		body string
		want string
	}{
		"too many":         {action("ctrl+g", "tab", "") + action("ctrl+l", "tab", "") + action("ctrl+q", "tab", "") + action("ctrl+s", "tab", ""), "at most 3"},
		"plain letter":     {action("f", "tab", ""), "must be ctrl+<letter>"},
		"upper case":       {action("ctrl+F", "tab", ""), "must be ctrl+<letter>"},
		"shift modifier":   {action("shift+f", "tab", ""), "must be ctrl+<letter>"},
		"built-in key":     {action("ctrl+o", "tab", ""), "already used by nexo"},
		"terminal alias":   {action("ctrl+i", "tab", ""), "already used by nexo"},
		"search edit key":  {action("ctrl+f", "tab", ""), "already used by nexo"},
		"search word key":  {action("alt+b", "tab", ""), "already used by nexo"},
		"duplicate key":    {action("ctrl+s", "tab", "") + action("ctrl+s", "float", ""), "bound twice"},
		"unknown behavior": {action("ctrl+s", "split-left", ""), "unknown behavior"},
		"empty behavior":   {action("ctrl+s", "", ""), "unknown behavior"},
		"long label":       {action("ctrl+s", "tab", "new-window"), "wider than 8"},
		"wide label":       {action("ctrl+s", "tab", "分割窓口画面"), "wider than 8"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadTUIConfig(t, test.body)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "tui.project-actions") {
				t.Fatalf("Load() error = %v, want %q for tui.project-actions", err, test.want)
			}
		})
	}
}

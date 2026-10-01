package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// restoreDarkBackground pins the adaptive variant for a test and restores it
// afterwards, so watcher assertions do not leak into the shared lipgloss state.
func restoreDarkBackground(t *testing.T, dark bool) {
	t.Helper()
	previous := lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetHasDarkBackground(previous) })
	lipgloss.SetHasDarkBackground(dark)
}

func writeThemeConfig(t *testing.T, body string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	rewriteThemeConfig(t, path, body, modTime)
	return path
}

func rewriteThemeConfig(t *testing.T, path, body string, modTime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func themeModel(t *testing.T, path, theme string, read func(string) ([]byte, error)) Model {
	t.Helper()
	return NewModel(context.Background(), Deps{ConfigPath: path, Theme: theme, ThemeRead: read}, AgentsTab)
}

// pollTheme runs one watcher poll and applies an accepted result, mirroring
// what Bubble Tea does after the poll command resolves.
func pollTheme(t *testing.T, model Model) (Model, tea.Msg) {
	t.Helper()
	if model.theme == nil {
		t.Fatal("model has no theme watcher")
	}
	msg := model.theme.poll()
	if msg == nil {
		return model, nil
	}
	updated, _ := model.Update(msg)
	return updated.(Model), msg
}

func TestThemeWatchReloadsChangedTheme(t *testing.T) {
	restoreDarkBackground(t, true)
	path := writeThemeConfig(t, "[tui]\ntheme = \"dark\"\n", time.Now())
	model := themeModel(t, path, "dark", nil)
	model, _ = pollTheme(t, model) // first observation only records the baseline

	rewriteThemeConfig(t, path, "[tui]\ntheme = \"light\"\n", time.Now().Add(2*time.Second))
	_, msg := pollTheme(t, model)
	reload, ok := msg.(themeReloadMsg)
	if !ok || reload.theme != "light" {
		t.Fatalf("poll message = %#v, want a light reload", msg)
	}
	if lipgloss.HasDarkBackground() {
		t.Fatal("changed theme did not switch the adaptive variant to light")
	}
}

func TestThemeWatchDetectsReplacedInode(t *testing.T) {
	restoreDarkBackground(t, true)
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("[tui]\ntheme = \"dark\"\n")
	replacement := []byte("[tui]\ntheme = \"light\"\n")
	for len(original) < len(replacement) {
		original = append(original, ' ')
	}
	stamp := time.Unix(1_700_000_000, 0)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	model := themeModel(t, path, "dark", nil)
	model, _ = pollTheme(t, model)

	// A file replaced with mv keeps a new inode but can preserve size and
	// mtime, so os.SameFile is the only signal that it changed.
	tmp := path + ".new"
	if err := os.WriteFile(tmp, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tmp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	_, msg := pollTheme(t, model)
	reload, ok := msg.(themeReloadMsg)
	if !ok || reload.theme != "light" {
		t.Fatalf("poll message = %#v, want the replacement reloaded", msg)
	}
	if lipgloss.HasDarkBackground() {
		t.Fatal("mv-replaced config was not followed")
	}
}

func TestThemeWatchIgnoresUnchangedFile(t *testing.T) {
	restoreDarkBackground(t, true)
	reads := 0
	read := func(path string) ([]byte, error) {
		reads++
		return os.ReadFile(path)
	}
	path := writeThemeConfig(t, "[tui]\ntheme = \"dark\"\n", time.Now())
	model := themeModel(t, path, "dark", read)
	model, _ = pollTheme(t, model)
	if reads != 1 {
		t.Fatalf("first observation read the file %d times, want one", reads)
	}
	for i := 0; i < 3; i++ {
		model, _ = pollTheme(t, model)
	}
	if reads != 1 {
		t.Fatalf("unchanged file was read %d times, want no reload", reads)
	}
}

// TestThemeWatchAppliesAFileThatAppearsAfterLoad covers a config created (or
// edited) between the initial load and the first poll: the first observation
// still reads once, so the new value is not missed until the next change.
func TestThemeWatchAppliesAFileThatAppearsAfterLoad(t *testing.T) {
	restoreDarkBackground(t, true)
	path := filepath.Join(t.TempDir(), "config.toml")
	model := themeModel(t, path, "auto", nil)
	model, _ = pollTheme(t, model) // file is missing, so there is no baseline

	rewriteThemeConfig(t, path, "[tui]\ntheme = \"light\"\n", time.Now())
	if _, msg := pollTheme(t, model); msg == nil {
		t.Fatal("a config created after startup was not followed")
	}
	if lipgloss.HasDarkBackground() {
		t.Fatal("created config did not switch the adaptive variant")
	}
}

func TestThemeWatchKeepsThemeOnBadContent(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
	}{
		{name: "invalid value", contents: "[tui]\ntheme = \"system\"\n"},
		{name: "broken toml", contents: "[tui"},
	} {
		t.Run(test.name, func(t *testing.T) {
			restoreDarkBackground(t, true)
			path := writeThemeConfig(t, "[tui]\ntheme = \"dark\"\n", time.Now())
			model := themeModel(t, path, "dark", nil)
			model, _ = pollTheme(t, model)

			rewriteThemeConfig(t, path, test.contents, time.Now().Add(2*time.Second))
			model, msg := pollTheme(t, model)
			reload, ok := msg.(themeReloadMsg)
			if !ok || reload.theme != "" || reload.toast == "" {
				t.Fatalf("bad content poll = %#v, want a toast and no theme", msg)
			}
			if !lipgloss.HasDarkBackground() {
				t.Fatal("bad content changed the adaptive variant")
			}

			// The same bad content under a fresh stamp must stay silent rather
			// than toast every tick.
			rewriteThemeConfig(t, path, test.contents, time.Now().Add(3*time.Second))
			if _, msg := pollTheme(t, model); msg != nil {
				t.Fatalf("repeated bad content produced %#v, want silence", msg)
			}
		})
	}
}

func TestThemeWatchKeepsThemeOnAuto(t *testing.T) {
	restoreDarkBackground(t, true)
	path := writeThemeConfig(t, "[tui]\ntheme = \"dark\"\n", time.Now())
	model := themeModel(t, path, "dark", nil)
	model, _ = pollTheme(t, model)

	// Switch to light first so "kept" is distinguishable from the initial dark.
	rewriteThemeConfig(t, path, "[tui]\ntheme = \"light\"\n", time.Now().Add(2*time.Second))
	model, _ = pollTheme(t, model)

	rewriteThemeConfig(t, path, "[tui]\ntheme = \"auto\"\n", time.Now().Add(3*time.Second))
	if _, msg := pollTheme(t, model); msg != nil {
		t.Fatalf("auto produced %#v, want no theme change", msg)
	}
	if lipgloss.HasDarkBackground() {
		t.Fatal("auto mid-session changed the light variant")
	}
}

func TestThemeReloadMessageAppliesVariant(t *testing.T) {
	restoreDarkBackground(t, true)
	model := NewModel(context.Background(), Deps{}, AgentsTab)
	updated, _ := model.Update(themeReloadMsg{theme: "light", toast: "theme reload: bad content"})
	model = updated.(Model)
	if lipgloss.HasDarkBackground() {
		t.Fatal("reload message did not switch the adaptive variant")
	}
	if model.toast != "theme reload: bad content" {
		t.Fatalf("toast = %q, want the reload status", model.toast)
	}
}

func TestThemeWatcherSchedulesItsNextPoll(t *testing.T) {
	path := writeThemeConfig(t, "[tui]\ntheme = \"dark\"\n", time.Now())
	model := themeModel(t, path, "dark", nil)
	if cmd := model.Init(); cmd == nil {
		t.Fatal("Init scheduled no theme poll")
	}
	if _, cmd := model.Update(themeTickMsg{}); cmd == nil {
		t.Fatal("theme tick scheduled no follow-up poll")
	}
}

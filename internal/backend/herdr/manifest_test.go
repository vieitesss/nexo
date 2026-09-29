package herdr

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

type pluginManifest struct {
	ID              string            `toml:"id"`
	Name            string            `toml:"name"`
	Version         string            `toml:"version"`
	MinHerdrVersion string            `toml:"min_herdr_version"`
	Platforms       []string          `toml:"platforms"`
	Actions         []pluginAction    `toml:"actions"`
	Panes           []pluginPane      `toml:"panes"`
	Events          []pluginEventHook `toml:"events"`
}

type pluginAction struct {
	ID       string   `toml:"id"`
	Title    string   `toml:"title"`
	Contexts []string `toml:"contexts"`
	Command  []string `toml:"command"`
}

type pluginPane struct {
	ID        string   `toml:"id"`
	Title     string   `toml:"title"`
	Placement string   `toml:"placement"`
	Width     string   `toml:"width"`
	Height    string   `toml:"height"`
	Command   []string `toml:"command"`
}

type pluginEventHook struct {
	On      string   `toml:"on"`
	Command []string `toml:"command"`
}

func TestHerdrPluginManifestDeclaresPopupActions(t *testing.T) {
	manifestBytes := readRepoFile(t, "herdr-plugin.toml")
	var manifest pluginManifest
	if err := toml.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse herdr-plugin.toml: %v", err)
	}

	if manifest.ID != "nexo" || manifest.Name != "nexo" {
		t.Fatalf("plugin identity = %q/%q, want nexo/nexo", manifest.ID, manifest.Name)
	}
	if manifest.Version == "" || manifest.MinHerdrVersion != "0.9.0" {
		t.Fatalf("plugin versions = %q/%q, want non-empty/0.9.0", manifest.Version, manifest.MinHerdrVersion)
	}
	if !sameStrings(manifest.Platforms, []string{"linux", "macos"}) {
		t.Fatalf("plugin platforms = %#v, want linux and macos", manifest.Platforms)
	}
	if len(manifest.Events) != 0 {
		t.Fatalf("events = %#v, want no event hooks", manifest.Events)
	}

	wantActions := []pluginAction{
		{
			ID:       "place-picker",
			Title:    "Open nexo place picker",
			Contexts: []string{"workspace", "tab", "pane"},
			Command:  []string{"herdr", "plugin", "pane", "open", "--plugin", "nexo", "--entrypoint", "place-picker"},
		},
		{
			ID:       "agent-navigator",
			Title:    "Open nexo agent navigator",
			Contexts: []string{"workspace", "tab", "pane"},
			Command:  []string{"herdr", "plugin", "pane", "open", "--plugin", "nexo", "--entrypoint", "agent-navigator"},
		},
	}
	if len(manifest.Actions) != len(wantActions) {
		t.Fatalf("actions = %#v, want %#v", manifest.Actions, wantActions)
	}
	for index, want := range wantActions {
		got := manifest.Actions[index]
		if got.ID != want.ID || got.Title != want.Title || !sameStrings(got.Contexts, want.Contexts) || !sameStrings(got.Command, want.Command) {
			t.Errorf("action %d = %#v, want %#v", index, got, want)
		}
	}

	wantPanes := []pluginPane{
		{ID: "place-picker", Title: "nexo places", Placement: "popup", Width: "80%", Height: "80%", Command: []string{"nexo", "--backend=herdr"}},
		{ID: "agent-navigator", Title: "nexo agents", Placement: "popup", Width: "80%", Height: "80%", Command: []string{"nexo", "--backend=herdr", "agents", "pick"}},
	}
	if len(manifest.Panes) != len(wantPanes) {
		t.Fatalf("panes = %#v, want %#v", manifest.Panes, wantPanes)
	}
	for index, want := range wantPanes {
		got := manifest.Panes[index]
		if got.ID != want.ID || got.Title != want.Title || got.Placement != want.Placement || got.Width != want.Width || got.Height != want.Height || !sameStrings(got.Command, want.Command) {
			t.Errorf("pane %d = %#v, want %#v", index, got, want)
		}
	}

	if strings.Contains(string(manifestBytes), "[[keys.command]]") {
		t.Fatal("herdr-plugin.toml must not declare unsupported key tables")
	}
}

func TestHerdrDocsProvidePluginActionKeys(t *testing.T) {
	docs := string(readRepoFile(t, filepath.Join("docs", "herdr.md")))
	for _, snippet := range []string{
		"key = \"ctrl+s\"\ntype = \"plugin_action\"\ncommand = \"nexo.place-picker\"",
		"key = \"prefix+a\"\ntype = \"plugin_action\"\ncommand = \"nexo.agent-navigator\"",
		"herdr plugin install vieitesss/nexo",
		"herdr plugin link ~/personal/nexo",
		"existing `nexo` executable",
		"docs/release.md",
		"herdr server reload-config",
	} {
		if !strings.Contains(docs, snippet) {
			t.Errorf("docs/herdr.md does not contain %q", snippet)
		}
	}
}

func readRepoFile(t *testing.T, name string) []byte {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "..", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return contents
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

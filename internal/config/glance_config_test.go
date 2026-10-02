package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestLoadParsesGlanceSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[glance]\nfields = [\"target\", \"branch\"]\ntint = \"@thm_surface_0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got.Glance.Fields, []string{"target", "branch"}) || got.Glance.Tint != "@thm_surface_0" {
		t.Fatalf("Glance = %#v, want configured fields and tint", got.Glance)
	}
}

func TestLoadDefaultsGlanceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("backend = \"tmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got.Glance.Fields, []string{"dot", "target"}) || got.Glance.Tint != "" {
		t.Fatalf("Glance = %#v, want dot+target and no tint", got.Glance)
	}
}

// TestGlanceOrderedFields pins that enabled fields render in the canonical
// order, not the order they appear in the file.
func TestGlanceOrderedFields(t *testing.T) {
	cfg := GlanceConfig{Fields: []string{"age", "target", "dot", "branch"}}
	want := []string{"dot", "target", "branch", "age"}
	if got := cfg.OrderedFields(); !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedFields() = %v, want %v", got, want)
	}
	if got := (GlanceConfig{}).OrderedFields(); !reflect.DeepEqual(got, []string{"dot", "target"}) {
		t.Fatalf("OrderedFields() default = %v, want dot+target", got)
	}
}

func TestLoadAcceptsGlanceTints(t *testing.T) {
	for _, value := range []string{"", "default", "red", "brightwhite", "colour123", "#ff0000", "@thm_surface_0", "@theme-bg"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[glance]\ntint = "+strconv.Quote(value)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("Load() error = %v, want %q accepted", err, value)
			}
		})
	}
}

func TestLoadRejectsInvalidGlanceTint(t *testing.T) {
	for _, value := range []string{"not-a-colour", "colour256", "#ff00", "@", "@thm surface", "bg=red"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[glance]\ntint = "+strconv.Quote(value)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), "glance.tint") {
				t.Fatalf("Load() error = %v, want glance.tint validation", err)
			}
		})
	}
}

func TestLoadRejectsInvalidGlanceFields(t *testing.T) {
	for _, value := range []string{"[\"dot\", \"nope\"]", "[\"dot\", \"dot\"]", "[\"\"]"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[glance]\nfields = "+value+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), "glance.fields") {
				t.Fatalf("Load() error = %v, want glance.fields validation", err)
			}
		})
	}
}

func TestLoadRejectsUnknownGlanceKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[glance]\ncolour = \"red\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "glance.colour") {
		t.Fatalf("Load() error = %v, want unknown key path glance.colour", err)
	}
}

func TestLoadParsesGlanceDotsAndColors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[glance]\ndots = \"symbols\"\ncolors = { working = \"@thm_yellow\", unseen = \"colour160\" }\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := GlanceColors{Working: "@thm_yellow", Unseen: "colour160", Seen: "green"}
	if got.Glance.Dots != GlanceDotsSymbols || got.Glance.Colors != want {
		t.Fatalf("Glance = %#v, want symbols and %#v", got.Glance, want)
	}
}

func TestLoadDefaultsGlanceDotsAndColors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[glance]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := GlanceColors{Working: "yellow", Unseen: "red", Seen: "green"}
	if got.Glance.Dots != GlanceDotsColor || got.Glance.Colors != want {
		t.Fatalf("Glance = %#v, want color dots and %#v", got.Glance, want)
	}
}

func TestLoadRejectsInvalidGlanceDots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[glance]\ndots = \"emoji\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "glance.dots") {
		t.Fatalf("Load() error = %v, want glance.dots validation", err)
	}
}

func TestLoadRejectsInvalidGlanceColors(t *testing.T) {
	for _, key := range []string{"working", "unseen", "seen"} {
		for _, value := range []string{"not-a-colour", "colour256", "@", "fg=red"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.toml")
				contents := "[glance.colors]\n" + key + " = " + strconv.Quote(value) + "\n"
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := Load(path)
				if err == nil || !strings.Contains(err.Error(), "glance.colors."+key) {
					t.Fatalf("Load() error = %v, want glance.colors.%s validation", err, key)
				}
			})
		}
	}
}

func TestLoadRejectsUnknownGlanceColorKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[glance.colors]\nidle = \"red\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "glance.colors.idle") {
		t.Fatalf("Load() error = %v, want unknown key path glance.colors.idle", err)
	}
}

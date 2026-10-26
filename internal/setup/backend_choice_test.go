package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installedRunner answers `command -v` for the multiplexers in installed.
type installedRunner map[string]bool

func (r installedRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "sh" && len(args) == 2 && strings.HasPrefix(args[1], "command -v ") && !r[strings.TrimPrefix(args[1], "command -v ")] {
		return nil, errors.New("not found")
	}
	return nil, nil
}

func TestChooseBackendAsksOnlyWhenNothingDecides(t *testing.T) {
	both := installedRunner{"tmux": true, "herdr": true}
	for _, test := range []struct {
		name        string
		in          Input
		options     Options
		want, about string
	}{
		{"flag", Input{Runner: both, TTY: true}, Options{Backend: "herdr"}, "herdr", "set by --backend"},
		{"config", Input{Runner: both, TTY: true, SelectedBackend: "tmux", SelectedSource: "config"}, Options{}, "tmux", "set in config"},
		{"inside a multiplexer", Input{Runner: both, TTY: true, SelectedBackend: "herdr", SelectedSource: "auto-detect"}, Options{}, "herdr", "detected in this terminal"},
		{"only tmux", Input{Runner: installedRunner{"tmux": true}, TTY: true, SelectedBackend: "none"}, Options{}, "tmux", "the only one installed"},
		{"only herdr", Input{Runner: installedRunner{"herdr": true}, TTY: true, SelectedBackend: "none"}, Options{}, "herdr", "the only one installed"},
		{"both installed", Input{Runner: both, TTY: true, SelectedBackend: "none"}, Options{}, "tmux", ""},
	} {
		got, about, err := chooseBackend(context.Background(), test.in, test.options)
		if err != nil || got != test.want || about != test.about {
			t.Errorf("%s: chooseBackend = %q, %q, %v; want %q, %q", test.name, got, about, err, test.want, test.about)
		}
	}
}

func TestChooseBackendDoesNotReplaceRex(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		in           Input
		options      Options
	}{
		{"config", "set in config", Input{Runner: installedRunner{"tmux": true}, SelectedBackend: "rex", SelectedSource: "config"}, Options{}},
		{"flag", "set by --backend", Input{Runner: installedRunner{"tmux": true}}, Options{Backend: "rex"}},
		{"detected", "detected in this terminal", Input{Runner: installedRunner{"tmux": true}, SelectedBackend: "rex", SelectedSource: "auto-detect"}, Options{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, reason, err := chooseBackend(context.Background(), test.in, test.options)
			if err != nil || backend != "rex" || reason != test.reason {
				t.Fatalf("chooseBackend() = %q, %q, %v; want rex, %q", backend, reason, err, test.reason)
			}
		})
	}
}

func TestSetupLeavesBackendToRuntimeDetection(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	notify := true
	err := Run(context.Background(), Input{Runner: &statefulLocalRunner{tmuxWatcher: true}, Home: home, Stdout: &bytes.Buffer{}}, Options{Backend: "tmux", ConfigPath: configPath, LocalOnly: true, Yes: true, Notify: &notify})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "backend") {
		t.Fatalf("setup pinned the backend in config:\n%s", got)
	}
}

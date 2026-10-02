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

type statefulLocalRunner struct {
	herdrPlugin bool
	tmuxWatcher bool
	calls       []string
}

func (r *statefulLocalRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	switch {
	case name == "sh" && len(args) >= 2 && strings.Contains(args[len(args)-1], "pgrep"):
		if r.tmuxWatcher {
			return nil, nil
		}
		return nil, errors.New("watcher missing")
	case name == "herdr" && len(args) >= 1 && args[0] == "--version":
		return []byte("herdr 0.9.0"), nil
	case name == "herdr" && len(args) >= 2 && args[0] == "plugin" && args[1] == "list":
		if r.herdrPlugin {
			return []byte("vieitesss/nexo"), nil
		}
		return nil, nil
	case name == "herdr" && len(args) >= 3 && args[0] == "plugin" && args[1] == "install":
		r.herdrPlugin = true
		return nil, nil
	case name == "tmux" && len(args) >= 1 && args[0] == "source-file":
		r.tmuxWatcher = true
		return nil, nil
	case name == "herdr" && len(args) >= 3 && args[0] == "server":
		return nil, nil
	default:
		return nil, nil
	}
}

func TestSetupRerunPreservesBothTmuxBindings(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	confPath := filepath.Join(home, ".tmux.conf")
	if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(confPath, []byte(ManagedStart+"\n"+TmuxBlock(false, "C-s", "a")+"\n"+ManagedEnd+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &statefulLocalRunner{tmuxWatcher: true}
	notify := true
	for run := 0; run < 2; run++ {
		if err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, LocalOnly: true, Yes: true, Notify: &notify}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	tuiKey, agentsKey := bindingsFromContents(string(got), "tmux")
	if tuiKey != "C-s" || agentsKey != "a" {
		t.Fatalf("rerun changed bindings: %q, %q\n%s", tuiKey, agentsKey, got)
	}
}

func TestSetupRejectsInvalidTmuxBindingBeforeWrites(t *testing.T) {
	for _, key := range []string{"Return", "^^a"} {
		home := t.TempDir()
		configPath := filepath.Join(home, ".config", "nexo", "config.toml")
		confPath := filepath.Join(home, ".tmux.conf")
		original := []byte(ManagedStart + "\n" + TmuxBlock(false, key, "a") + "\n" + ManagedEnd + "\n")
		if err := os.WriteFile(confPath, original, 0o600); err != nil {
			t.Fatal(err)
		}
		runner := &statefulLocalRunner{tmuxWatcher: true}
		notify := true
		err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, LocalOnly: true, Yes: true, Notify: &notify})
		if err == nil {
			t.Fatalf("setup accepted tmux key %q", key)
		}
		got, readErr := os.ReadFile(confPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, original) {
			t.Fatalf("setup rewrote .tmux.conf for invalid key %q:\n%s", key, got)
		}
		if _, statErr := os.Stat(configPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("setup wrote config for invalid key %q: %v", key, statErr)
		}
	}
}

func TestSetupRerunPreservesHerdrBindings(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	herdrPath := filepath.Join(home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(herdrPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdrPath, []byte(ManagedStart+"\n"+HerdrBlock("ctrl+s", "prefix+a")+"\n"+ManagedEnd+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &statefulLocalRunner{herdrPlugin: true}
	notify := true
	for run := 0; run < 2; run++ {
		if err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "herdr", ConfigPath: configPath, LocalOnly: true, Yes: true, Notify: &notify}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(herdrPath)
	if err != nil {
		t.Fatal(err)
	}
	tuiKey, agentsKey := bindingsFromContents(string(got), "herdr")
	if tuiKey != "ctrl+s" || agentsKey != "prefix+a" {
		t.Fatalf("rerun changed bindings: %q, %q\n%s", tuiKey, agentsKey, got)
	}
}

func TestCheckModeIsReadOnly(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("backend = \"tmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &statefulLocalRunner{tmuxWatcher: true}
	before, _ := os.ReadFile(configPath)
	_ = Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, LocalOnly: true, Check: true})
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("check modified config: before=%q after=%q", before, after)
	}
	if _, err := os.Stat(configPath + ".nexo.bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check created backup: %v", err)
	}
}

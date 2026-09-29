package tmux

import (
	"context"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestInitInstallsBindingsHooksAndWatcher(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	if err := backend.Init(context.Background(), config.TmuxConfig{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"unbind-key", "-n", "C-s"}},
		{name: "tmux", args: []string{"bind-key", "-n", "C-s", "display-popup", "-E", "-w", "80%", "-h", "80%", "-x", "C", "-y", "C", "nexo --backend=tmux"}},
		{name: "tmux", args: []string{"unbind-key", "a"}},
		{name: "tmux", args: []string{"bind-key", "a", "display-popup", "-E", "-w", "80%", "-h", "80%", "-x", "C", "-y", "C", "nexo --backend=tmux agents pick"}},
		{name: "tmux", args: []string{"show-option", "-gqv", "focus-events"}},
		{name: "tmux", args: []string{"set-option", "-g", "focus-events", "on"}},
		{name: "tmux", args: []string{"set-hook", "-g", "pane-focus-in", "run-shell 'nexo --backend tmux agents seen #{pane_id}'"}},
		{name: "tmux", args: []string{"set-hook", "-g", "client-session-changed", "run-shell 'nexo --backend tmux agents seen #{pane_id}'"}},
		{name: "tmux", args: []string{"run-shell", "-b", "nexo --backend=tmux agents watch"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestInitUnbindsDefaultsWhenKeysAreDisabled(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	cfg := config.TmuxConfig{PlaceKey: "none", AgentKey: "none"}
	if err := backend.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if current.name == "tmux" && current.args[0] == "bind-key" {
			t.Fatalf("calls = %#v, want no bindings", runner.calls)
		}
	}
}

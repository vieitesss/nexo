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
	if err := backend.Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"show-option", "-gqv", "@nexo-place-key"}},
		{name: "tmux", args: []string{"list-keys", "-T", "root", "C-s"}},
		{name: "tmux", args: []string{"bind-key", "-n", "C-s", "display-popup", "-E", "-w", "80%", "-h", "80%", "-x", "C", "-y", "C", pickerCommand("")}},
		{name: "tmux", args: []string{"set-option", "-g", "@nexo-place-key", "root:C-s"}},
		{name: "tmux", args: []string{"show-option", "-gqv", "@nexo-agent-key"}},
		{name: "tmux", args: []string{"list-keys", "-T", "prefix", "a"}},
		{name: "tmux", args: []string{"bind-key", "a", "display-popup", "-E", "-w", "80%", "-h", "80%", "-x", "C", "-y", "C", agentPickerCommand("")}},
		{name: "tmux", args: []string{"set-option", "-g", "@nexo-agent-key", "prefix:a"}},
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
	if err := backend.Init(context.Background(), InitInput{Config: cfg}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if current.name == "tmux" && current.args[0] == "bind-key" {
			t.Fatalf("calls = %#v, want no bindings", runner.calls)
		}
	}
}

func TestInitPreservesUserBindingsAndReportsConflicts(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-keys -T root C-s":         []byte("bind-key -T root C-s display-message user\n"),
		"list-keys -T prefix a":         []byte("bind-key -T prefix a display-message user\n"),
		"show-option -gqv focus-events": []byte("on\n"),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if current.name == "tmux" && current.args[0] == "bind-key" {
			t.Fatalf("calls = %#v, want existing bindings preserved", runner.calls)
		}
	}
	messages := 0
	for _, current := range runner.calls {
		if current.name == "tmux" && len(current.args) > 0 && current.args[0] == "display-message" {
			messages++
		}
	}
	if messages != 2 {
		t.Fatalf("display-message calls = %d, want one per conflict", messages)
	}
}

func TestInitRemovesRecordedBindingsWhenKeysChange(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte("root:C-s\n"),
		"show-option -gqv @nexo-agent-key": []byte("prefix:a\n"),
		"list-keys -T root C-s":            []byte("bind-key -T root C-s display-popup NEXO_PLACE_PICKER_INLINE=1\n"),
		"list-keys -T prefix a":            []byte("bind-key -T prefix a display-popup NEXO_AGENT_NAVIGATOR_INLINE=1\n"),
		"show-option -gqv focus-events":    []byte("on\n"),
	}}
	cfg := config.TmuxConfig{PlaceKey: "C-x", AgentKey: "none"}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: cfg}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "unbind-key", "-n", "C-s") || !hasCall(runner.calls, "unbind-key", "a") {
		t.Fatalf("calls = %#v, want recorded bindings removed", runner.calls)
	}
	if !hasCall(runner.calls, "bind-key", "-n", "C-x") {
		t.Fatalf("calls = %#v, want new place binding", runner.calls)
	}
}

func hasCall(calls []call, name string, args ...string) bool {
	for _, current := range calls {
		if current.name != "tmux" || len(current.args) < 1+len(args) || current.args[0] != name {
			continue
		}
		if reflect.DeepEqual(current.args[1:1+len(args)], args) {
			return true
		}
	}
	return false
}

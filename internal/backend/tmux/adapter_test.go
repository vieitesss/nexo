package tmux

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestInitInstallsBindingsHooksAndWatcher(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	if err := backend.Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	focusCommand := focusHookCommand(os.Args[0], "")
	watchCommand := watcherCommand("")
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
		{name: "tmux", args: []string{"show-hooks", "-g", "pane-focus-in"}},
		{name: "tmux", args: []string{"set-hook", "-ag", "pane-focus-in", focusCommand}},
		{name: "tmux", args: []string{"show-hooks", "-g", "client-session-changed"}},
		{name: "tmux", args: []string{"set-hook", "-ag", "client-session-changed", focusCommand}},
		{name: "tmux", args: []string{"run-shell", "-b", watchCommand}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestInitUsesSelectedBinaryAndConfigForWatcherAndHooks(t *testing.T) {
	runner := &fakeRunner{}
	configPath := "/tmp/custom config.toml"
	if err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{PlaceKey: "none", AgentKey: "none"}, ConfigPath: configPath}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "run-shell", "-b", watcherCommand(configPath)) {
		t.Fatalf("calls = %#v, want configured watcher command", runner.calls)
	}
	if !hasCall(runner.calls, "set-hook", "-ag", "pane-focus-in", focusHookCommand(os.Args[0], configPath)) || !hasCall(runner.calls, "set-hook", "-ag", "client-session-changed", focusHookCommand(os.Args[0], configPath)) {
		t.Fatalf("calls = %#v, want configured focus hooks", runner.calls)
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

func TestNexoBindingDetectionFailsClosed(t *testing.T) {
	validPlace := "bind-key -T root C-s display-popup -E -h 80% -w 80% -x C -y C \"NEXO_PLACE_PICKER_INLINE=1 'nexo' --backend='tmux'\""
	validAgent := "bind-key -T prefix a display-popup -E -h 80% -w 80% -x C -y C \"NEXO_AGENT_NAVIGATOR_INLINE=1 'nexo' --backend='tmux' agents pick\""
	for _, test := range []struct {
		name    string
		binding string
		want    bool
	}{
		{name: "place popup", binding: validPlace, want: true},
		{name: "agent popup", binding: validAgent, want: true},
		{name: "arbitrary backend command", binding: `bind-key -T root C-s run-shell "nexo --backend=tmux agents --json"`},
		{name: "marker message", binding: `bind-key -T root C-s display-message "NEXO_PLACE_PICKER_INLINE=1 is a marker"`},
		{name: "marker prefix", binding: `bind-key -T root C-s display-popup "NEXO_PLACE_PICKER_INLINE=10 'nexo' --backend='tmux'"`},
		{name: "marker phrase", binding: `bind-key -T root C-s display-popup "NEXO_PLACE_PICKER_INLINE=1 is a marker"`},
		{name: "arbitrary marked subcommand", binding: `bind-key -T root C-s display-popup "NEXO_PLACE_PICKER_INLINE=1 'nexo' --backend='tmux' agents --json"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isNexoBinding(test.binding); got != test.want {
				t.Fatalf("isNexoBinding() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestInitRejectsRecordedUnsupportedTable(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte("custom:C-s\n"),
	}}
	err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{PlaceKey: "none", AgentKey: "none"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported recorded tmux key table") {
		t.Fatalf("Init() error = %v, want unsupported table", err)
	}
	if hasCall(runner.calls, "unbind-key") {
		t.Fatalf("calls = %#v, want no unbind for unsupported table", runner.calls)
	}
}

func TestInitPreservesIndexedHooks(t *testing.T) {
	focus := "pane-focus-in[0] run-shell \"user focus\"\npane-focus-in[1] run-shell \"NEXO_AGENT_FOCUS_HOOK=1 'nexo' --backend='tmux' agents seen #{pane_id}\"\npane-focus-in[4] run-shell \"other focus\"\n"
	session := "client-session-changed[0] run-shell \"user session\"\n"
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv focus-events":        []byte("on\n"),
		"show-hooks -g pane-focus-in":          []byte(focus),
		"show-hooks -g client-session-changed": []byte(session),
		"show-option -gqv @nexo-place-key":     []byte("\n"),
		"show-option -gqv @nexo-agent-key":     []byte("\n"),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{PlaceKey: "none", AgentKey: "none"}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "set-hook", "-g", "pane-focus-in[1]", focusHookCommand(os.Args[0], "")) || !hasCall(runner.calls, "set-hook", "-ag", "client-session-changed", focusHookCommand(os.Args[0], "")) {
		t.Fatalf("calls = %#v, want indexed update and append", runner.calls)
	}
	if hasCall(runner.calls, "set-hook", "-g", "pane-focus-in[0]", focusHookCommand(os.Args[0], "")) || hasCall(runner.calls, "set-hook", "-g", "pane-focus-in[4]", focusHookCommand(os.Args[0], "")) {
		t.Fatalf("calls = %#v, want user hook indexes preserved", runner.calls)
	}
}

func TestInitRemovesRecordedBindingsWhenKeysChange(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte("root:C-s\n"),
		"show-option -gqv @nexo-agent-key": []byte("prefix:a\n"),
		"list-keys -T root C-s":            []byte("bind-key -T root C-s display-popup -E -h 80% -w 80% -x C -y C \"NEXO_PLACE_PICKER_INLINE=1 'nexo' --backend='tmux'\"\n"),
		"list-keys -T prefix a":            []byte("bind-key -T prefix a display-popup -E -h 80% -w 80% -x C -y C \"NEXO_AGENT_NAVIGATOR_INLINE=1 'nexo' --backend='tmux' agents pick\"\n"),
		"show-option -gqv focus-events":    []byte("on\n"),
	}}
	cfg := config.TmuxConfig{PlaceKey: "C-x", AgentKey: "none"}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: cfg}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "unbind-key", "-T", "root", "C-s") || !hasCall(runner.calls, "unbind-key", "-T", "prefix", "a") {
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

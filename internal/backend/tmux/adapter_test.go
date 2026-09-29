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
	placeLine := "bind-key -T root C-s display-popup -E -h 100% -w 100% -x C -y C \"" + pickerCommand("") + "\"\n"
	agentLine := "bind-key -T prefix a display-popup -E -h 100% -w 100% -x C -y C \"" + agentPickerCommand("") + "\"\n"
	runner := &fakeRunner{sequences: map[string][][]byte{
		"list-keys -T root C-s": {nil, []byte(placeLine)},
		"list-keys -T prefix a": {nil, []byte(agentLine)},
	}}
	backend := New(runner, true)
	if err := backend.Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	focusCommand := focusHookCommand(os.Args[0], "")
	watchCommand := watcherCommand("")
	if !hasCall(runner.calls, "bind-key", "-n", "C-s", "display-popup", "-E", "-w", "100%", "-h", "100%", "-x", "C", "-y", "C", pickerCommand("")) || !hasCall(runner.calls, "bind-key", "a", "display-popup", "-E", "-w", "100%", "-h", "100%", "-x", "C", "-y", "C", agentPickerCommand("")) {
		t.Fatalf("calls = %#v, want configured bindings", runner.calls)
	}
	if !hasCall(runner.calls, "set-option", "-g", "@nexo-place-key", encodeBindingRecord("root", "C-s", placeLine)) || !hasCall(runner.calls, "set-option", "-g", "@nexo-agent-key", encodeBindingRecord("prefix", "a", agentLine)) {
		t.Fatalf("calls = %#v, want exact binding records", runner.calls)
	}
	if hasCall(runner.calls, "set-option", "-g", "focus-events", "on") {
		t.Fatalf("calls = %#v, want no focus-events mutation", runner.calls)
	}
	if !hasCall(runner.calls, "display-message", "nexo: agent seen-marking needs 'set -g focus-events on'") {
		t.Fatalf("calls = %#v, want focus-events warning", runner.calls)
	}
	if !hasCall(runner.calls, "set-hook", "-ag", "pane-focus-in", focusCommand) || !hasCall(runner.calls, "set-hook", "-ag", "client-session-changed", focusCommand) || !hasCall(runner.calls, "run-shell", "-b", watchCommand) {
		t.Fatalf("calls = %#v, want hooks and watcher", runner.calls)
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

func TestBindingRecordRoundTripsExactLine(t *testing.T) {
	line := "bind-key -T root ; display-popup \"/tmp/agent's config\"\n"
	value := encodeBindingRecord("root", ";", line)
	record, ok, err := decodeBindingRecord(value)
	if err != nil || !ok {
		t.Fatalf("decodeBindingRecord() = %#v, %v, want record", record, err)
	}
	if record.table != "root" || record.key != ";" || record.line != line {
		t.Fatalf("record = %#v, want exact table/key/line", record)
	}
}

func TestInitWarnsAndContinuesForUnverifiedRecordedBinding(t *testing.T) {
	placeLine := "bind-key -T root C-x display-popup -E -h 100% -w 100% -x C -y C \"" + pickerCommand("") + "\"\n"
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"show-option -gqv @nexo-place-key": []byte("root:C-s\n"),
			"show-option -gqv focus-events":    []byte("on\n"),
		},
		sequences: map[string][][]byte{
			"list-keys -T root C-x": {nil, []byte(placeLine)},
		},
	}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{PlaceKey: "C-x", AgentKey: "none"}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "display-message", "nexo: couldn't verify its previous binding for key \"C-s\"; left it alone") {
		t.Fatalf("calls = %#v, want unverified-binding warning", runner.calls)
	}
	if !hasCall(runner.calls, "bind-key", "-n", "C-x") {
		t.Fatalf("calls = %#v, want configured key installed", runner.calls)
	}
	for _, current := range runner.calls {
		if current.name == "tmux" && len(current.args) >= 4 && current.args[0] == "unbind-key" {
			t.Fatalf("calls = %#v, want no unbind for unverified record", runner.calls)
		}
	}
}

func TestInitWarnsForUnverifiedBindingWhenHooksAlreadyExist(t *testing.T) {
	hook := "pane-focus-in[0] run-shell \"NEXO_AGENT_FOCUS_HOOK=1 'nexo' --backend='tmux' agents seen #{pane_id}\"\n"
	sessionHook := "client-session-changed[0] run-shell \"NEXO_AGENT_FOCUS_HOOK=1 'nexo' --backend='tmux' agents seen #{pane_id}\"\n"
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv focus-events":        []byte("off\n"),
		"show-hooks -g pane-focus-in":          []byte(hook),
		"show-hooks -g client-session-changed": []byte(sessionHook),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{PlaceKey: "none", AgentKey: "none"}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "display-message", "nexo: agent seen-marking needs 'set -g focus-events on'") {
		t.Fatalf("calls = %#v, want focus-events warning with existing hooks", runner.calls)
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
	placeLine := "bind-key -T root C-s display-popup -E -h 100% -w 100% -x C -y C \"" + pickerCommand("") + "\"\n"
	agentLine := "bind-key -T prefix a display-popup -E -h 100% -w 100% -x C -y C \"" + agentPickerCommand("") + "\"\n"
	newPlaceLine := "bind-key -T root C-x display-popup -E -h 100% -w 100% -x C -y C \"" + pickerCommand("") + "\"\n"
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte(encodeBindingRecord("root", "C-s", placeLine)),
		"show-option -gqv @nexo-agent-key": []byte(encodeBindingRecord("prefix", "a", agentLine)),
		"list-keys -T root C-s":            []byte(placeLine),
		"list-keys -T prefix a":            []byte(agentLine),
		"show-option -gqv focus-events":    []byte("on\n"),
	}, sequences: map[string][][]byte{
		"list-keys -T root C-x": {nil, []byte(newPlaceLine)},
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

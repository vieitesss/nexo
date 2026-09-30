package tmux

import (
	"context"
	"reflect"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestInitInstallsHooksAndWatcherWithoutBindings(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv focus-events": []byte("on\n"),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if len(current.args) > 0 && current.args[0] == "bind-key" {
			t.Fatalf("calls = %#v, want no key bindings", runner.calls)
		}
	}
	if !hasCall(runner.calls, "run-shell", "-b", watcherCommand("")) {
		t.Fatalf("calls = %#v, want agent watcher", runner.calls)
	}
}

func TestInitRemovesOwnedBindingsAndRecords(t *testing.T) {
	placeLine := "bind-key -T root C-s display-popup old\n"
	agentLine := "bind-key -T prefix a display-popup old\n"
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte(encodeBindingRecord("root", "C-s", placeLine)),
		"show-option -gqv @nexo-agent-key": []byte(encodeBindingRecord("prefix", "a", agentLine)),
		"list-keys -T root C-s":            []byte(placeLine),
		"list-keys -T prefix a":            []byte(agentLine),
		"show-option -gqv focus-events":    []byte("on\n"),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "unbind-key", "-T", "root", "C-s") || !hasCall(runner.calls, "unbind-key", "-T", "prefix", "a") {
		t.Fatalf("calls = %#v, want owned bindings removed", runner.calls)
	}
	if !hasCall(runner.calls, "set-option", "-g", "@nexo-place-key", "") || !hasCall(runner.calls, "set-option", "-g", "@nexo-agent-key", "") {
		t.Fatalf("calls = %#v, want ownership records deleted", runner.calls)
	}
}

func TestInitKeepsUserModifiedBinding(t *testing.T) {
	owned := "bind-key -T root C-s display-popup old\n"
	changed := "bind-key -T root C-s display-message user\n"
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte(encodeBindingRecord("root", "C-s", owned)),
		"list-keys -T root C-s":            []byte(changed),
		"show-option -gqv focus-events":    []byte("on\n"),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if hasCall(runner.calls, "unbind-key", "-T", "root", "C-s") {
		t.Fatalf("calls = %#v, want user-modified binding kept", runner.calls)
	}
	if !hasCall(runner.calls, "set-option", "-g", "@nexo-place-key", "") {
		t.Fatalf("calls = %#v, want ownership record deleted", runner.calls)
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

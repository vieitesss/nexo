package tmux

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func encodeBindingRecord(table, key, line string) string {
	return strings.Join([]string{
		bindingRecordVersion,
		table,
		base64.RawStdEncoding.EncodeToString([]byte(key)),
		base64.RawStdEncoding.EncodeToString([]byte(line)),
	}, ":")
}

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
	if !hasCall(runner.calls, "set-option", "-gu", "@nexo-place-key") || !hasCall(runner.calls, "set-option", "-gu", "@nexo-agent-key") {
		t.Fatalf("calls = %#v, want ownership records unset", runner.calls)
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
	if !hasCall(runner.calls, "set-option", "-gu", "@nexo-place-key") {
		t.Fatalf("calls = %#v, want ownership record unset", runner.calls)
	}
}

func TestInitWarnsAndUnsetsUnverifiableRecord(t *testing.T) {
	var stderr strings.Builder
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte("legacy-key\n"),
		"show-option -gqv @nexo-agent-key": []byte("v1:window:!!:!!\n"),
		"show-option -gqv focus-events":    []byte("on\n"),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{Stderr: &stderr}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if got := strings.Count(stderr.String(), "nexo no longer binds keys"); got != 2 {
		t.Fatalf("warning count = %d, stderr = %q; want one per record", got, stderr.String())
	}
	if hasCall(runner.calls, "list-keys") || hasCall(runner.calls, "unbind-key") {
		t.Fatalf("calls = %#v, want unverifiable bindings left untouched", runner.calls)
	}
	if !hasCall(runner.calls, "set-option", "-gu", "@nexo-place-key") || !hasCall(runner.calls, "set-option", "-gu", "@nexo-agent-key") {
		t.Fatalf("calls = %#v, want unverifiable records unset", runner.calls)
	}
}

func TestInitRemovesOwnedBindingWithSpecialKey(t *testing.T) {
	line := "bind-key -T root C-\\; display-message 'quoted value'\n"
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv @nexo-place-key": []byte(encodeBindingRecord("root", "C-;", line)),
		"list-keys -T root C-\\;":          []byte(line),
	}}
	if err := New(runner, true).Init(context.Background(), InitInput{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !hasCall(runner.calls, "unbind-key", "-T", "root", `C-\;`) {
		t.Fatalf("calls = %#v, want escaped special key removal", runner.calls)
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

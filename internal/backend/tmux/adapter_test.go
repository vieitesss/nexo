package tmux

import (
	"context"
	"reflect"
	"testing"
)

func TestInitInstallsHooksAndWatcherWithoutBindings(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"show-option -gqv focus-events": []byte("on\n")}}
	if err := New(runner, true).Init(context.Background(), InitInput{}); err != nil {
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

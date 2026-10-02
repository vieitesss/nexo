package tmux

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestInitInstallsHooksAndWatcherWithoutBindings(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"show-option -gqv focus-events": []byte("on\n")}}
	if err := New(runner, true).Init(context.Background(), capabilities.InitInput{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if len(current.args) > 1 && current.args[1] == "bind-key" {
			t.Fatalf("calls = %#v, want no key bindings", runner.calls)
		}
	}
	if !hasCall(runner.calls, "run-shell", "-b", watcherCommand("")) {
		t.Fatalf("calls = %#v, want agent watcher", runner.calls)
	}
}

func TestWatcherCommandUsesAbsoluteLaunchPathForSlashlessArgv0(t *testing.T) {
	dir := t.TempDir()
	path := executableFile(t, dir, "nexo", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir)
	original := os.Args
	os.Args = []string{"nexo"}
	defer func() { os.Args = original }()

	command := watcherCommand("")
	if !strings.HasPrefix(command, shellQuote(path)) {
		t.Fatalf("watcherCommand() = %q, want it to start with absolute %q", command, path)
	}
}

func hasCall(calls []call, name string, args ...string) bool {
	for _, current := range calls {
		if current.name != "tmux" || len(current.args) < 2+len(args) || current.args[0] != "-u" || current.args[1] != name {
			continue
		}
		if reflect.DeepEqual(current.args[2:2+len(args)], args) {
			return true
		}
	}
	return false
}

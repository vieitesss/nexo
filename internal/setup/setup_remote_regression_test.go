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

type sequencedRemoteRunner struct {
	probe []byte
	calls int
}

func (r *sequencedRemoteRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ssh" {
		return nil, nil
	}
	r.calls++
	command := strings.Join(args, " ")
	if strings.Contains(command, "home=%s") {
		return r.probe, nil
	}
	if strings.Contains(command, "--version") {
		return []byte("v1.0.0"), nil
	}
	if strings.Contains(command, "__NEXO_FILE_PRESENT__") {
		return []byte("__NEXO_FILE_MISSING__\n"), nil
	}
	return nil, nil
}

func TestPlanRemoteIncludesRepairForStoppedWatcher(t *testing.T) {
	runner := &sequencedRemoteRunner{probe: []byte("home=/home/u\nshell=/bin/zsh\nplatform=Linux x86_64\nnexo=/home/u/.local/bin/nexo\ntmux=/usr/bin/tmux\nherdr=\nplugin=no\nlocale=UTF-8\nutf8locale=C.UTF-8\nwatcher=no\ntmux_running=yes\nherdr_running=no\n")}
	plan := planRemote(context.Background(), Input{Runner: runner}, Options{}, "dev", "", "tmux", nil)
	if plan.Error != nil {
		t.Fatal(plan.Error)
	}
	if !containsAction(plan.Actions, "initialize watcher") || !containsAction(plan.Actions, "start tmux watcher") {
		t.Fatalf("stopped watcher repair missing: %#v", plan.Actions)
	}
}

func TestDryRunPlanningFailureReturnsError(t *testing.T) {
	// An unreachable machine must not be hidden by the dry-run success path.
	// The local plan is still displayed and no config is written.
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	notify := true
	output := &bytes.Buffer{}
	runner := &recordingReadRunner{err: errors.New("offline")}
	err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: output, TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, Machines: []string{"offline"}, DryRun: true, Yes: true, Notify: &notify})
	if err == nil {
		t.Fatal("dry-run concealed unreachable remote")
	}
	if _, statErr := os.Stat(configPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dry-run wrote config: %v", statErr)
	}
	if !strings.Contains(output.String(), "offline: failed") {
		t.Fatalf("dry-run did not print remote failure: %q", output)
	}
}

func containsAction(actions []string, needle string) bool {
	for _, action := range actions {
		if strings.Contains(action, needle) {
			return true
		}
	}
	return false
}

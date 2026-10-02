package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

type workflowMachine struct {
	home   string
	files  map[string][]byte
	binary bool
}

type workflowSSHRunner struct {
	localHome string
	machines  map[string]*workflowMachine
	calls     []string
	writes    int
}

func newWorkflowSSHRunner(home string) *workflowSSHRunner {
	return &workflowSSHRunner{localHome: home, machines: map[string]*workflowMachine{
		"good": {home: "/home/good", files: map[string][]byte{}},
		"bad":  {home: "/home/bad", files: map[string][]byte{}},
	}}
}

func (r *workflowSSHRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "ssh" {
		machine := args[len(args)-2]
		if machine == "bad" {
			return nil, errors.New("offline")
		}
		return r.remote(machine, args[len(args)-1])
	}
	if name == "sh" {
		command := args[len(args)-1]
		switch {
		case strings.Contains(command, "command -v nexo"):
			return []byte("/tmp/local/nexo\n"), nil
		case strings.Contains(command, "locale charmap"):
			return []byte("UTF-8\n"), nil
		case strings.Contains(command, "tmux show-options"):
			return []byte("watcher=yes\ntmux_running=yes\ntmux_focus=on\ntmux_hooks=yes\n"), nil
		case strings.Contains(command, "pgrep"):
			return nil, nil
		}
	}
	if name == "tmux" || name == "/tmp/local/nexo" {
		return []byte("nexo v1.0.0\n"), nil
	}
	return nil, nil
}

func (r *workflowSSHRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" [input] "+strings.Join(args, " "))
	if name != "ssh" {
		return nil, nil
	}
	r.writes++
	machine := args[len(args)-2]
	state := r.machines[machine]
	command := args[len(args)-1]
	if strings.Contains(command, ".nexo-copy.") {
		state.binary = true
		return nil, nil
	}
	for _, path := range []string{".zshenv", ".bashrc", ".tmux.conf", ".config/herdr/config.toml"} {
		if strings.Contains(command, path) {
			state.files[state.home+"/"+path] = append([]byte(nil), input...)
			return nil, nil
		}
	}
	return nil, nil
}

func (r *workflowSSHRunner) remote(machine, command string) ([]byte, error) {
	state := r.machines[machine]
	switch {
	case strings.Contains(command, "home=%s"):
		binary := ""
		if state.binary {
			binary = state.home + "/.local/bin/nexo"
		}
		return []byte(fmt.Sprintf("home=%s\nshell=/bin/zsh\nplatform=%s %s\nnexo=%s\ntmux=/usr/bin/tmux\nherdr=\nplugin=no\nlocale=UTF-8\nutf8locale=C.UTF-8\nwatcher=yes\ntmux_running=yes\ntmux_focus=on\ntmux_hooks=yes\nherdr_running=no\n", state.home, runtime.GOOS, runtime.GOARCH, binary)), nil
	case strings.Contains(command, "--version"):
		return []byte("nexo v1.0.0\n"), nil
	case strings.Contains(command, "__NEXO_FILE_PRESENT__"):
		for path, contents := range state.files {
			if strings.Contains(command, path) || strings.Contains(command, filepath.Base(path)) {
				return append([]byte("__NEXO_FILE_PRESENT__\n"), contents...), nil
			}
		}
		return []byte("__NEXO_FILE_MISSING__\n"), nil
	case strings.Contains(command, "command -v nexo"):
		if state.binary {
			return []byte(state.home + "/.local/bin/nexo\n"), nil
		}
		return nil, errors.New("nexo missing")
	case strings.Contains(command, "locale charmap"):
		return []byte("UTF-8\n"), nil
	default:
		return nil, nil
	}
}

func TestSetupStatefulRemoteWorkflowAndMixedDryRun(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "nexo", "config.toml")
	runner := newWorkflowSSHRunner(home)
	notify := true
	options := Options{Backend: "tmux", ConfigPath: configPath, Machines: []string{"good"}, Yes: true, Notify: &notify}
	if err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: options.Backend, ConfigPath: options.ConfigPath, Machines: options.Machines, Yes: true, DryRun: true, Notify: &notify}); err != nil {
		t.Fatal(err)
	}
	if runner.writes != 0 || hasWorkflowWriteCommand(runner.calls) {
		t.Fatalf("dry-run issued write commands: writes=%d calls=%v", runner.writes, runner.calls)
	}
	if err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, options); err != nil {
		t.Fatal(err)
	}
	beforeRerun := runner.writes
	if err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, options); err != nil {
		t.Fatal(err)
	}
	if runner.writes != beforeRerun {
		t.Fatalf("unchanged rerun wrote remote files: before=%d after=%d", beforeRerun, runner.writes)
	}
	beforeCheck := runner.writes
	beforeCheckCalls := len(runner.calls)
	if err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, Machines: []string{"good"}, Check: true, Notify: &notify}); err != nil {
		t.Fatal(err)
	}
	if runner.writes != beforeCheck || hasWorkflowWriteCommand(runner.calls[beforeCheckCalls:]) {
		t.Fatalf("check issued write commands")
	}
	beforeMixed := runner.writes
	err := Run(context.Background(), Input{Runner: runner, Home: home, Stdout: &bytes.Buffer{}, TTY: false}, Options{Backend: "tmux", ConfigPath: configPath, Machines: []string{"good", "bad"}, Yes: true, DryRun: true, Notify: &notify})
	if err == nil || runner.writes != beforeMixed {
		t.Fatalf("mixed dry-run = %v, writes before=%d after=%d", err, beforeMixed, runner.writes)
	}
}

func hasWorkflowWriteCommand(calls []string) bool {
	for _, call := range calls {
		if strings.Contains(call, "[input]") || strings.Contains(call, "source-file") || strings.Contains(call, "tmux init") || strings.Contains(call, "reload-config") {
			return true
		}
	}
	return false
}

func containsAction(actions []string, needle string) bool {
	for _, action := range actions {
		if strings.Contains(action, needle) {
			return true
		}
	}
	return false
}

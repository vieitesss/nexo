package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls            []call
	interactiveCalls int
	interactiveCheck func()
	fail             map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	key := strings.Join(args, " ")
	if err, ok := f.fail[key]; ok {
		return []byte(err.Error()), err
	}
	return nil, nil
}

func (f *fakeRunner) RunInteractive(_ context.Context, name string, args ...string) error {
	f.interactiveCalls++
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	if f.interactiveCheck != nil {
		f.interactiveCheck()
	}
	key := strings.Join(args, " ")
	return f.fail[key]
}

func TestSessionNameUsesParentAndBaseAndIsTmuxSafe(t *testing.T) {
	if got, want := sessionName("/work/acme/my.project:dev"), "acme_my_project_dev"; got != want {
		t.Fatalf("sessionName() = %q, want %q", got, want)
	}
}

func TestOpenProjectSwitchesToExistingExactSessionInsideTmux(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{{name: "tmux", args: []string{"has-session", "-t", "=acme_app"}}, {name: "tmux", args: []string{"switch-client", "-t", "=acme_app"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOpenProjectCreatesAndAttachesMissingSessionOutsideTmux(t *testing.T) {
	runner := &fakeRunner{fail: map[string]error{
		"has-session -t =acme_app": errors.New("can't find session: acme_app"),
	}}
	backend := New(runner, false)
	if err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{}); err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{{name: "tmux", args: []string{"has-session", "-t", "=acme_app"}}, {name: "tmux", args: []string{"new-session", "-d", "-s", "acme_app", "-c", "/work/acme/app"}}, {name: "tmux", args: []string{"attach-session", "-t", "=acme_app"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if runner.interactiveCalls != 1 {
		t.Fatalf("interactive calls = %d, want 1", runner.interactiveCalls)
	}
}

func TestOpenProjectReportsInteractiveAttachError(t *testing.T) {
	want := errors.New("open terminal failed: not a terminal")
	runner := &fakeRunner{fail: map[string]error{
		"attach-session -t =acme_app": want,
	}}
	backend := New(runner, false)
	err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{})
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) || !strings.Contains(tmuxErr.Stderr, "not a terminal") {
		t.Fatalf("error = %v, want typed interactive error", err)
	}
	if runner.interactiveCalls != 1 {
		t.Fatalf("interactive calls = %d, want 1", runner.interactiveCalls)
	}
}

func TestOpenProjectReportsTypedTmuxErrorWithStderr(t *testing.T) {
	want := errors.New("permission denied")
	runner := &fakeRunner{fail: map[string]error{
		"has-session -t =acme_app": want,
	}}
	backend := New(runner, true)
	err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{})
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) || !strings.Contains(tmuxErr.Stderr, "permission denied") {
		t.Fatalf("error = %v, want typed error carrying stderr", err)
	}
}

func seedStoppedAgent(t *testing.T, backend *Backend, paneID string) {
	t.Helper()
	backend.agents.SetStateDir(t.TempDir())
	backend.agents.server = "test-server"
	if err := backend.agents.updateStates(func(states map[string]agentState) error {
		states[paneID] = agentState{State: "stopped"}
		return nil
	}); err != nil {
		t.Fatalf("seed agent state: %v", err)
	}
}

func TestFocusPaneSwitchesContainerWindowAndPaneByExactID(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	seedStoppedAgent(t, backend, "%7")
	if err := backend.FocusPane(context.Background(), "%7"); err != nil {
		t.Fatalf("FocusPane() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"switch-client", "-t", "%7"}},
		{name: "tmux", args: []string{"select-window", "-t", "%7"}},
		{name: "tmux", args: []string{"select-pane", "-t", "%7"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	states, err := backend.agents.readStates()
	if err != nil || !states["%7"].Seen {
		t.Fatalf("agent state after focus = %#v, err=%v; want seen", states["%7"], err)
	}
}

func TestFocusPaneSelectsAndAttachesOutsideTmux(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, false)
	seedStoppedAgent(t, backend, "%7")
	seenBeforeAttach := false
	runner.interactiveCheck = func() {
		states, err := backend.agents.readStates()
		if err == nil {
			seenBeforeAttach = states["%7"].Seen
		}
	}
	if err := backend.FocusPane(context.Background(), "%7"); err != nil {
		t.Fatalf("FocusPane() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"select-window", "-t", "%7"}},
		{name: "tmux", args: []string{"select-pane", "-t", "%7"}},
		{name: "tmux", args: []string{"attach-session", "-t", "%7"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if runner.interactiveCalls != 1 {
		t.Fatalf("interactive calls = %d, want one attach", runner.interactiveCalls)
	}
	if !seenBeforeAttach {
		t.Fatal("agent was not marked seen before blocking attach")
	}
}

func TestOpenProjectRejectsContainerSelectionOnTmux(t *testing.T) {
	backend := New(&fakeRunner{})
	err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "container-1"})
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %v, want UnsupportedError", err)
	}
	if !strings.Contains(err.Error(), "--workspace-id") {
		t.Fatalf("error = %v, want workspace-id detail", err)
	}
}

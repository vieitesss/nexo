package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/agentstate"
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
	outputs          map[string][]byte
	sequences        map[string][][]byte
	outputFunc       func(string, []string) ([]byte, bool)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	key := commandKey(args)
	if err, ok := f.fail[key]; ok {
		return []byte(err.Error()), err
	}
	if sequence := f.sequences[key]; len(sequence) > 0 {
		output := sequence[0]
		f.sequences[key] = sequence[1:]
		return output, nil
	}
	if f.outputFunc != nil {
		if output, ok := f.outputFunc(name, args); ok {
			return output, nil
		}
	}
	return f.outputs[key], nil
}

// commandKey builds the fake's lookup key. The global -u flag that nexo always
// passes to tmux is ignored for lookups so table tests stay focused on the
// operation; raw arguments are still recorded in calls for exact assertions.
func commandKey(args []string) string {
	return strings.TrimPrefix(strings.Join(args, " "), "-u ")
}

func (f *fakeRunner) RunInteractive(_ context.Context, name string, args ...string) error {
	f.interactiveCalls++
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	if f.interactiveCheck != nil {
		f.interactiveCheck()
	}
	key := commandKey(args)
	return f.fail[key]
}

func TestListContainersUsesStableIDsAndActivePane(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-sessions -F #{session_id}\t#{session_name}\t#{session_activity}\t#{@nexo-proxy}": []byte("$1\tdev\t0\t\n$2\twork\t0\t\n"),
		"list-panes -t $1 -F #{pane_id}\t#{pane_active}":                                       []byte("%1\t0\n%2\t1\n"),
		"list-panes -t $2 -F #{pane_id}\t#{pane_active}":                                       []byte("%3\t1\n"),
	}}
	backend := New(runner)
	containers, err := backend.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 2 || containers[0].ID != "$1" || containers[0].Name != "dev" || containers[0].ActivePaneID != "%2" || containers[1].ActivePaneID != "%3" {
		t.Fatalf("containers = %#v, want stable IDs and active panes", containers)
	}
}

func TestContainerActionsUseBackendNativeCommands(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	runner := &fakeRunner{}
	backend := New(runner, true)
	if err := backend.OpenContainer(context.Background(), "$1"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}
	if err := backend.RenameContainer(context.Background(), "$1", "renamed"); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if err := backend.KillContainers(context.Background(), []string{"$1", "$2"}); err != nil {
		t.Fatalf("KillContainers() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"switch-client", "-t", "$1"}},
		{name: "tmux", args: []string{"-u", "rename-session", "-t", "$1", "renamed"}},
		{name: "tmux", args: []string{"-u", "kill-session", "-t", "$1"}},
		{name: "tmux", args: []string{"-u", "kill-session", "-t", "$2"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestKillContainersKillsCurrentSessionLast(t *testing.T) {
	t.Setenv("TMUX_PANE", "%current")
	runner := &fakeRunner{outputs: map[string][]byte{
		"display-message -p -t %current #{session_id}": []byte("$1\n"),
	}}
	if err := New(runner, true).KillContainers(context.Background(), []string{"$1", "$2"}); err != nil {
		t.Fatalf("KillContainers() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "%current", "#{session_id}"}},
		{name: "tmux", args: []string{"-u", "kill-session", "-t", "$2"}},
		{name: "tmux", args: []string{"-u", "kill-session", "-t", "$1"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want current session last: %#v", runner.calls, want)
	}
}

func TestReadScreenCapturesPane(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{"capture-pane -p -t %2": []byte("hello\n")}}
	backend := New(runner)
	got, err := backend.ReadScreen(context.Background(), "%2")
	if err != nil || got != "hello\n" {
		t.Fatalf("ReadScreen() = %q, %v", got, err)
	}
}

func TestOpenProjectSwitchesToExistingExactSessionInsideTmux(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"display-message -p -t =acme_app: #{session_id}": []byte("$12\n"),
	}}
	backend := New(runner, true)
	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{{name: "tmux", args: []string{"-u", "has-session", "-t", "=acme_app"}}, {name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "=acme_app:", "#{session_id}"}}, {name: "tmux", args: []string{"switch-client", "-t", "=acme_app"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if container.ID != "$12" || container.Name != "acme_app" || container.Path != "/work/acme/app" || container.Created {
		t.Fatalf("container = %#v, want existing session $12", container)
	}
}

func TestOpenProjectNoFocusReusesExistingSessionInsideTmux(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"display-message -p -t =acme_app: #{session_id}": []byte("$5\n"),
	}}
	backend := New(runner, true)
	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"-u", "has-session", "-t", "=acme_app"}},
		{name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "=acme_app:", "#{session_id}"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want no switch-client: %#v", runner.calls, want)
	}
	if container.ID != "$5" || container.Created {
		t.Fatalf("container = %#v, want reused session $5", container)
	}
}

func TestOpenProjectNoFocusCreatesDetachedSessionOutsideTmux(t *testing.T) {
	runner := &fakeRunner{
		fail: map[string]error{
			"has-session -t =acme_app": errors.New("can't find session: acme_app"),
		},
		outputs: map[string][]byte{
			"display-message -p -t =acme_app: #{session_id}": []byte("$9\n"),
		},
	}
	backend := New(runner, false)
	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{NoFocus: true})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"-u", "has-session", "-t", "=acme_app"}},
		{name: "tmux", args: []string{"-u", "new-session", "-d", "-s", "acme_app", "-c", "/work/acme/app"}},
		{name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "=acme_app:", "#{session_id}"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want detached session without attach: %#v", runner.calls, want)
	}
	if runner.interactiveCalls != 0 {
		t.Fatalf("interactive calls = %d, want no attach", runner.interactiveCalls)
	}
	if !container.Created || container.ID != "$9" || container.Name != "acme_app" {
		t.Fatalf("container = %#v, want created session $9", container)
	}
}

func TestOpenProjectCreatesAndAttachesMissingSessionOutsideTmux(t *testing.T) {
	runner := &fakeRunner{
		fail: map[string]error{
			"has-session -t =acme_app": errors.New("can't find session: acme_app"),
		},
		outputs: map[string][]byte{
			"display-message -p -t =acme_app: #{session_id}": []byte("$12\n"),
		},
	}
	backend := New(runner, false)
	container, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenProject() error = %v", err)
	}
	want := []call{{name: "tmux", args: []string{"-u", "has-session", "-t", "=acme_app"}}, {name: "tmux", args: []string{"-u", "new-session", "-d", "-s", "acme_app", "-c", "/work/acme/app"}}, {name: "tmux", args: []string{"-u", "display-message", "-p", "-t", "=acme_app:", "#{session_id}"}}, {name: "tmux", args: []string{"attach-session", "-t", "=acme_app"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if runner.interactiveCalls != 1 {
		t.Fatalf("interactive calls = %d, want 1", runner.interactiveCalls)
	}
	if !container.Created || container.ID != "$12" {
		t.Fatalf("container = %#v, want created session $12", container)
	}
}

func TestOpenProjectReportsInteractiveAttachError(t *testing.T) {
	want := errors.New("open terminal failed: not a terminal")
	runner := &fakeRunner{fail: map[string]error{
		"attach-session -t =acme_app": want,
	}}
	backend := New(runner, false)
	_, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{})
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
	_, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{})
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) || !strings.Contains(tmuxErr.Stderr, "permission denied") {
		t.Fatalf("error = %v, want typed error carrying stderr", err)
	}
}

func seedStoppedAgent(t *testing.T, backend *Backend, paneID string) {
	t.Helper()
	setStateDir(backend.agents, t.TempDir())
	backend.agents.server = "test-server"
	if err := backend.agents.ledger().Update(func(document *agentstate.Document) error {
		document.Agents[paneID] = agentstate.State{State: "stopped"}
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
		{name: "tmux", args: []string{"-u", "select-window", "-t", "%7"}},
		{name: "tmux", args: []string{"-u", "select-pane", "-t", "%7"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	document, err := backend.agents.ledger().Read()
	if err != nil || !document.Agents["%7"].Seen {
		t.Fatalf("agent state after focus = %#v, err=%v; want seen", document.Agents["%7"], err)
	}
}

func TestFocusPaneSelectsAndAttachesOutsideTmux(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, false)
	seedStoppedAgent(t, backend, "%7")
	seenBeforeAttach := false
	runner.interactiveCheck = func() {
		document, err := backend.agents.ledger().Read()
		if err == nil {
			seenBeforeAttach = document.Agents["%7"].Seen
		}
	}
	if err := backend.FocusPane(context.Background(), "%7"); err != nil {
		t.Fatalf("FocusPane() error = %v", err)
	}
	want := []call{
		{name: "tmux", args: []string{"-u", "select-window", "-t", "%7"}},
		{name: "tmux", args: []string{"-u", "select-pane", "-t", "%7"}},
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
	_, err := backend.OpenProject(context.Background(), "/work/acme/app", capabilities.OpenOptions{ContainerID: "container-1"})
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %v, want UnsupportedError", err)
	}
	if !strings.Contains(err.Error(), "--workspace-id") {
		t.Fatalf("error = %v, want workspace-id detail", err)
	}
}

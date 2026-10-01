package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/agentstate"
	"github.com/vieitesss/nexo/internal/config"
)

func TestRenderHighlightStyle(t *testing.T) {
	for _, test := range []struct {
		name   string
		colour string
		want   string
	}{
		{name: "bare colour becomes a foreground", colour: "red", want: "fg=red"},
		{name: "palette name", colour: "colour123", want: "fg=colour123"},
		{name: "hex colour", colour: "#ff0000", want: "fg=#ff0000"},
		{name: "full style verbatim", colour: "bg=red,bold", want: "bg=red,bold"},
		{name: "empty falls back to red", colour: "", want: "fg=red"},
		{name: "whitespace is trimmed", colour: "  red  ", want: "fg=red"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := renderHighlightStyle(test.colour); got != test.want {
				t.Fatalf("renderHighlightStyle(%q) = %q, want %q", test.colour, got, test.want)
			}
		})
	}
}

func TestParseWindowHighlights(t *testing.T) {
	output := "@0\t\tdefault\n@1\t1\tfg=red\n@2\t\tbg=blue\n"
	got := parseWindowHighlights([]byte(output))
	want := []windowHighlight{
		{id: "@0", marked: false, style: "default"},
		{id: "@1", marked: true, style: "fg=red"},
		{id: "@2", marked: false, style: "bg=blue"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWindowHighlights() = %#v, want %#v", got, want)
	}
}

func TestHighlightActions(t *testing.T) {
	windows := []windowHighlight{
		{id: "@0", marked: false, style: "default"},
		{id: "@1", marked: true, style: "fg=red"},
		{id: "@2", marked: true, style: "fg=green"},
		{id: "@3", marked: false, style: "bg=blue"},
	}
	got := highlightActions(windows, []string{"@0", "@2"}, "fg=red")
	want := []highlightAction{
		{windowID: "@0", clear: false},
		{windowID: "@1", clear: true},
		{windowID: "@2", clear: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("highlightActions() = %#v, want %#v", got, want)
	}
}

func TestHighlightActionsKeepsMatchingHighlight(t *testing.T) {
	windows := []windowHighlight{
		{id: "@0", marked: true, style: "fg=red"},
		{id: "@1", marked: false, style: "default"},
	}
	if got := highlightActions(windows, []string{"@0"}, "fg=red"); got != nil {
		t.Fatalf("highlightActions() = %#v, want no changes", got)
	}
}

func TestUnseenWindows(t *testing.T) {
	observed := []observedPane{
		{pane: pane{id: "%1", windowID: "@0"}},
		{pane: pane{id: "%2", windowID: "@0"}},
		{pane: pane{id: "%3", windowID: "@1"}},
		{pane: pane{id: "%4", windowID: "@2"}},
		{pane: pane{id: "%5", windowID: "@3"}},
	}
	results := []agentstate.Result{
		{State: agentstate.State{State: "stopped", Seen: false}},
		{State: agentstate.State{State: "stopped", Seen: true}},
		{State: agentstate.State{State: "working", Seen: false}},
		{State: agentstate.State{State: "stopped", Seen: false}},
		{State: agentstate.State{State: "stopped", Seen: false}},
	}
	// "@3" briefly loses its pane between listing and capturing, so it is not
	// present in the results at all.
	got := unseenWindows(observed[:4], results[:4])
	want := []string{"@0", "@2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unseenWindows() = %#v, want %#v", got, want)
	}
}

func TestReconcileHighlightsSetsAndClearsWindows(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-windows -a -F #{window_id}\t#{@nexo-unseen}\t#{window-status-style}": []byte("@0\t\tdefault\n@1\t1\tfg=red\n@2\t\tbg=blue\n"),
	}}
	source := NewAgentSource(runner)
	source.ConfigureAgents(config.AgentsConfig{HighlightColor: "red"})
	if err := source.reconcileHighlights(context.Background(), []string{"@0"}); err != nil {
		t.Fatalf("reconcileHighlights() error = %v", err)
	}
	got := highlightCalls(runner.calls)
	want := [][]string{
		{"-u", "set-option", "-w", "-t", "@0", "window-status-style", "fg=red"},
		{"-u", "set-option", "-w", "-t", "@0", "window-status-current-style", "fg=red"},
		{"-u", "set-option", "-w", "-t", "@0", "@nexo-unseen", "1"},
		{"-u", "set-option", "-w", "-u", "-t", "@1", "window-status-style"},
		{"-u", "set-option", "-w", "-u", "-t", "@1", "window-status-current-style"},
		{"-u", "set-option", "-w", "-u", "-t", "@1", "@nexo-unseen"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("highlight calls = %#v, want %#v", got, want)
	}
}

func TestReconcileHighlightsRefreshesChangedColour(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-windows -a -F #{window_id}\t#{@nexo-unseen}\t#{window-status-style}": []byte("@0\t1\tfg=green\n"),
	}}
	source := NewAgentSource(runner)
	source.ConfigureAgents(config.AgentsConfig{HighlightColor: "red"})
	if err := source.reconcileHighlights(context.Background(), []string{"@0"}); err != nil {
		t.Fatalf("reconcileHighlights() error = %v", err)
	}
	got := highlightCalls(runner.calls)
	want := [][]string{
		{"-u", "set-option", "-w", "-t", "@0", "window-status-style", "fg=red"},
		{"-u", "set-option", "-w", "-t", "@0", "window-status-current-style", "fg=red"},
		{"-u", "set-option", "-w", "-t", "@0", "@nexo-unseen", "1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("highlight calls = %#v, want %#v", got, want)
	}
}

func TestReconcileHighlightsLeavesUnrelatedStylesAlone(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"list-windows -a -F #{window_id}\t#{@nexo-unseen}\t#{window-status-style}": []byte("@0\t\tbg=blue\n"),
	}}
	source := NewAgentSource(runner)
	if err := source.reconcileHighlights(context.Background(), nil); err != nil {
		t.Fatalf("reconcileHighlights() error = %v", err)
	}
	if got := highlightCalls(runner.calls); got != nil {
		t.Fatalf("highlight calls = %#v, want none for a window nexo never marked", got)
	}
}

func TestReconcileHighlightsReportsListError(t *testing.T) {
	failure := errors.New("no server")
	runner := &fakeRunner{fail: map[string]error{
		"list-windows -a -F #{window_id}\t#{@nexo-unseen}\t#{window-status-style}": failure,
	}}
	source := NewAgentSource(runner)
	err := source.reconcileHighlights(context.Background(), nil)
	if err == nil || !errors.Is(err, failure) {
		t.Fatalf("reconcileHighlights() error = %v, want list error", err)
	}
}

func TestReconcileHighlightsReportsSetError(t *testing.T) {
	failure := errors.New("invalid style")
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"list-windows -a -F #{window_id}\t#{@nexo-unseen}\t#{window-status-style}": []byte("@0\t\tdefault\n"),
		},
		fail: map[string]error{"set-option -w -t @0 window-status-style fg=red": failure},
	}
	source := NewAgentSource(runner)
	err := source.reconcileHighlights(context.Background(), []string{"@0"})
	if err == nil || !errors.Is(err, failure) {
		t.Fatalf("reconcileHighlights() error = %v, want set error", err)
	}
}

// TestPollHighlightsAndListingDoesNot pins the watcher-only seam: the plain
// agent listing must never touch the tmux window list, while the watcher's
// poll marks the window that holds an unseen stopped agent.
func TestPollHighlightsAndListingDoesNot(t *testing.T) {
	clock := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{outputFunc: func(name string, args []string) ([]byte, bool) {
		if name != "tmux" {
			return nil, false
		}
		switch key := commandKey(args); {
		case strings.HasPrefix(key, "list-panes"):
			return []byte("%1\t99\tpi\t$1\twork\teditor\t1\t0\t@1\t/tmp\n"), true
		case strings.HasPrefix(key, "list-windows"):
			return []byte("@1\t\tdefault\n"), true
		case strings.HasPrefix(key, "capture-pane"):
			return []byte("prompt> "), true
		}
		return nil, false
	}}
	source := NewAgentSource(runner)
	setStateDir(source, t.TempDir())
	setClock(source, func() time.Time { return clock })
	source.server = "/tmp/tmux-test"
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})
	if _, err := source.ListAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Second)

	runner.calls = nil
	agents, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].State != "stopped" || agents[0].Seen {
		t.Fatalf("agents = %#v, want one unseen stopped agent", agents)
	}
	if got := highlightCalls(runner.calls); got != nil {
		t.Fatalf("plain listing highlighted windows: %#v", got)
	}

	runner.calls = nil
	if _, err := source.listAgents(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	got := highlightCalls(runner.calls)
	want := [][]string{
		{"-u", "set-option", "-w", "-t", "@1", "window-status-style", "fg=red"},
		{"-u", "set-option", "-w", "-t", "@1", "window-status-current-style", "fg=red"},
		{"-u", "set-option", "-w", "-t", "@1", "@nexo-unseen", "1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("poll highlight calls = %#v, want %#v", got, want)
	}
}

// highlightCalls keeps only the tmux set-option calls reconcile issues so
// assertions read as the commands themselves.
func highlightCalls(calls []call) [][]string {
	var result [][]string
	for _, current := range calls {
		if current.name != "tmux" || len(current.args) < 3 || current.args[1] != "set-option" {
			continue
		}
		result = append(result, append([]string(nil), current.args...))
	}
	return result
}

package tmux

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/agentstate"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

func TestFindHarnessMatchesProcessArgs(t *testing.T) {
	allow := map[string]struct{}{"pi": {}, "claude": {}}
	for _, test := range []struct {
		name string
		args string
		want string
	}{
		{name: "node script", args: "node /opt/homebrew/bin/pi", want: "pi"},
		{name: "node flags", args: "node --no-warnings /x/pi", want: "pi"},
		{name: "claude resume", args: "/Users/x/.local/bin/claude --resume", want: "claude"},
		{name: "vim pi", args: "vim pi", want: ""},
		{name: "bash command string", args: `bash -c "claude --resume"`, want: ""},
		{name: "markdown file", args: "vim notes/claude.md", want: ""},
		{name: "bare interpreter", args: "node", want: ""},
		{name: "login shell", args: "-zsh", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := findHarness(test.args, allow); got != test.want {
				t.Fatalf("findHarness(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestFindPaneHarnessIsDepthFirstAndLowestPID(t *testing.T) {
	allow := map[string]struct{}{"pi": {}, "claude": {}}
	t.Run("bash child matches", func(t *testing.T) {
		tree := parseProcessTree([]byte(
			"  100     1 -zsh\n" +
				"  101   100 bash -c \"claude --resume\"\n" +
				"  102   101 claude --resume\n"))
		if got := findPaneHarness("100", tree, allow); got != "claude" {
			t.Fatalf("findPaneHarness() = %q, want claude", got)
		}
	})
	t.Run("lowest pid wins at same depth", func(t *testing.T) {
		tree := parseProcessTree([]byte(
			"  100     1 -zsh\n" +
				"  102   100 /x/claude\n" +
				"  101   100 /x/pi\n"))
		if got := findPaneHarness("100", tree, allow); got != "pi" {
			t.Fatalf("findPaneHarness() = %q, want lowest-pid pi", got)
		}
	})
}

func TestAgentPanesFindsHarnessInPaneDescendants(t *testing.T) {
	processes := "  100     1 -zsh\n" +
		"  101   100 node /opt/homebrew/bin/pi\n" +
		"  102   101 sleep 30\n" +
		"  200     1 -zsh\n" +
		"  201   200 /Users/x/.local/bin/claude --resume\n" +
		"  202   200 vim notes/claude.md\n" +
		"  300     1 -bash\n"
	panes := []string{
		"%1\t100\tnode\t$1\twork\teditor\t1\t0\t/tmp/work",
		"%2\t200\t2.1.30\t$2\tdev\tshell\t1\t0\t/tmp/dev",
		"%3\t300\tpi\t$3\tover\tmain\t0\t0\t/tmp/over",
		"%4\t202\tvim\t$4\tnotes\tmain\t0\t0\t/tmp/notes",
	}
	psCalls := 0
	r := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "tmux":
			return []byte(strings.Join(panes, "\n") + "\n"), nil
		case "ps":
			psCalls++
			for i, arg := range args {
				if arg != "-p" || i+1 >= len(args) {
					continue
				}
				for _, line := range strings.Split(processes, "\n") {
					fields := strings.Fields(line)
					if len(fields) > 2 && fields[0] == args[i+1] {
						return []byte(strings.Join(fields[2:], " ") + "\n"), nil
					}
				}
				return nil, nil
			}
			return []byte(processes), nil
		case "git":
			return []byte("\n"), nil
		default:
			return nil, nil
		}
	})
	source := NewAgentSource(r)
	got, err := source.agentPanes(context.Background(), config.AgentsConfig{Processes: []string{"pi", "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	harnesses := make([]string, 0, len(got))
	for _, current := range got {
		harnesses = append(harnesses, current.harness)
	}
	if !reflect.DeepEqual(harnesses, []string{"pi", "claude", "pi"}) {
		t.Fatalf("harnesses = %#v, want descendant and fast-path detection", harnesses)
	}
	if psCalls != 1 {
		t.Fatalf("ps calls = %d, want one snapshot per listing", psCalls)
	}
}

func TestOnlyOneWatcherLockPerServer(t *testing.T) {
	path := t.TempDir() + "/server.lock"
	release, err := acquireWatcherLock(path)
	if err != nil {
		t.Fatalf("first acquireWatcherLock() error = %v", err)
	}
	defer release()

	second, err := acquireWatcherLock(path)
	if second != nil {
		second()
	}
	if !errors.Is(err, errWatcherAlreadyRunning) {
		t.Fatalf("second acquireWatcherLock() error = %v, want already-running", err)
	}
}

func TestSourceTransitionsAfterIdleThreshold(t *testing.T) {
	clock := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	screen := "⠋ working"
	var blockCapture bool
	captureEntered := make(chan struct{})
	releaseCapture := make(chan struct{})
	var captureOnce sync.Once
	r := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "tmux":
			joined := commandKey(args)
			switch {
			case strings.HasPrefix(joined, "list-panes"):
				return []byte("%1\t99\tpi\t$1\twork\teditor\t1\t2\t/tmp\n"), nil
			case strings.HasPrefix(joined, "display-message"):
				return []byte("/tmp/tmux-test\n"), nil
			case strings.HasPrefix(joined, "capture-pane"):
				if blockCapture {
					captureOnce.Do(func() { close(captureEntered) })
					<-releaseCapture
				}
				return []byte(screen), nil
			}
		case "git":
			return []byte("feature/test\n"), nil
		}
		return nil, nil
	})
	source := NewAgentSource(r)
	setStateDir(source, t.TempDir())
	setClock(source, func() time.Time { return clock })
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, PollInterval: 1})

	agents, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].State != "working" || agents[0].ContainerID != "$1" {
		t.Fatalf("first state = %#v, want working agent with stable container ID", agents)
	}

	screen = "prompt> "
	clock = clock.Add(2 * time.Second)
	agents, err = source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if agents[0].State != "working" {
		t.Fatalf("state after two seconds = %#v, want working", agents[0])
	}

	clock = clock.Add(time.Second)
	agents, err = source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if agents[0].State != "stopped" || agents[0].StoppedSince == nil || !agents[0].StoppedSince.Equal(clock) {
		t.Fatalf("state after threshold = %#v, want stopped at %v", agents[0], clock)
	}
	if agents[0].Seen {
		t.Fatal("newly stopped pane is seen")
	}

	// A watcher may have a stale snapshot when the focus-hook process marks
	// the pane seen. Block the watcher in its fake capture runner, mark seen
	// from a second source, then release the watcher before its state update.
	watcher := NewAgentSource(r)
	setStateDir(watcher, source.stateDir)
	setClock(watcher, func() time.Time { return clock })
	watcher.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, PollInterval: 1})
	blockCapture = true
	watcherDone := make(chan struct{})
	var watcherAgents []capabilities.Agent
	var watcherErr error
	go func() {
		watcherAgents, watcherErr = watcher.ListAgents(context.Background())
		close(watcherDone)
	}()
	<-captureEntered

	hook := NewAgentSource(r)
	setStateDir(hook, source.stateDir)
	setClock(hook, func() time.Time { return clock })
	hook.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, PollInterval: 1})
	if err := hook.MarkSeen(context.Background(), "%1"); err != nil {
		t.Fatal(err)
	}
	close(releaseCapture)
	<-watcherDone
	if watcherErr != nil {
		t.Fatal(watcherErr)
	}
	if len(watcherAgents) != 1 || !watcherAgents[0].Seen {
		t.Fatalf("watcher poll clobbered focus hook state: %#v", watcherAgents)
	}
}

func TestMarkSeenUnknownPaneCreatesNoState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	r := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			return []byte("/tmp/tmux-test\n"), nil
		}
		return nil, nil
	})
	source := NewAgentSource(r)
	setStateDir(source, stateDir)
	if err := source.MarkSeen(context.Background(), "%999"); err != nil {
		t.Fatalf("MarkSeen() error = %v", err)
	}
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state dir = %v, want no artifacts", err)
	}
}

func TestMarkSeenUnknownPaneLeavesExistingStateByteIdentical(t *testing.T) {
	r := funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			return []byte("/tmp/tmux-test\n"), nil
		}
		return nil, nil
	})
	source := NewAgentSource(r)
	setStateDir(source, t.TempDir())
	source.server = "/tmp/tmux-test"
	if err := source.ledger().Update(func(document *agentstate.Document) error {
		document.Agents["%1"] = agentstate.State{State: "stopped"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source.statePath("json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := source.MarkSeen(context.Background(), "%999"); err != nil {
		t.Fatalf("MarkSeen() error = %v", err)
	}
	after, err := os.ReadFile(source.statePath("json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("state changed:\nbefore %q\nafter  %q", before, after)
	}
}

func TestMarkSeenMalformedStatePreservesReadError(t *testing.T) {
	source := NewAgentSource(funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		return nil, nil
	}))
	setStateDir(source, t.TempDir())
	source.server = "/tmp/tmux-test"
	if err := os.WriteFile(source.ledger().Path("json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := source.MarkSeen(context.Background(), "%1")
	const want = "read agent state: unexpected end of JSON input"
	if err == nil || err.Error() != want {
		t.Fatalf("MarkSeen() error = %v, want %q", err, want)
	}
}

func TestMarkSeenUpdateFailurePreservesLockError(t *testing.T) {
	source := NewAgentSource(funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		return nil, nil
	}))
	setStateDir(source, t.TempDir())
	source.server = "/tmp/tmux-test"
	if err := source.ledger().Update(func(document *agentstate.Document) error {
		document.Agents["%1"] = agentstate.State{State: "stopped"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lockPath := source.ledger().Path("state.lock")
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	err := source.MarkSeen(context.Background(), "%1")
	want := "lock agent state: open state lock: open " + lockPath + ": is a directory"
	if err == nil || err.Error() != want {
		t.Fatalf("MarkSeen() error = %v, want %q", err, want)
	}
}

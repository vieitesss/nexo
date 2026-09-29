package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

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
	r := runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "tmux":
			joined := strings.Join(args, " ")
			switch {
			case strings.HasPrefix(joined, "list-panes"):
				return []byte("%1\t99\tpi\twork\t1\t2\t/tmp\n"), nil
			case strings.HasPrefix(joined, "display-message"):
				return []byte("/tmp/tmux-test\n"), nil
			case strings.HasPrefix(joined, "capture-pane"):
				return []byte(screen), nil
			}
		case "git":
			return []byte("feature/test\n"), nil
		}
		return nil, nil
	})
	source := NewAgentSource(r)
	source.SetStateDir(t.TempDir())
	source.SetClock(func() time.Time { return clock })
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, PollInterval: 1})

	agents, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].State != "working" {
		t.Fatalf("first state = %#v, want working", agents)
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

	if err := source.MarkSeen(context.Background(), "%1"); err != nil {
		t.Fatal(err)
	}
	agents, err = source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !agents[0].Seen {
		t.Fatal("focused stopped pane is still unseen")
	}

	// A watcher process may have a stale in-memory snapshot when the focus
	// hook process marks the pane seen. Its next poll must merge that update
	// instead of overwriting it.
	watcher := NewAgentSource(r)
	watcher.SetStateDir(source.stateDir)
	watcher.SetClock(func() time.Time { return clock })
	watcher.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, PollInterval: 1})
	agents, err = watcher.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !agents[0].Seen {
		t.Fatal("watcher poll clobbered focus hook state")
	}
}

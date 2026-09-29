package tmux

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
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
	var blockCapture bool
	captureEntered := make(chan struct{})
	releaseCapture := make(chan struct{})
	var captureOnce sync.Once
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

	// A watcher may have a stale snapshot when the focus-hook process marks
	// the pane seen. Block the watcher in its fake capture runner, mark seen
	// from a second source, then release the watcher before its state update.
	watcher := NewAgentSource(r)
	watcher.SetStateDir(source.stateDir)
	watcher.SetClock(func() time.Time { return clock })
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
	hook.SetStateDir(source.stateDir)
	hook.SetClock(func() time.Time { return clock })
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

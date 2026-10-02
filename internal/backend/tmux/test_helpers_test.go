package tmux

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestMain keeps backend tests from writing container history into the
// developer's real XDG state directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nexo-tmux-test-state")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_STATE_HOME", dir)
	if os.Getenv("NEXO_TEST_WATCHER_HELPER") == "1" {
		release, err := acquireWatcherLock(os.Getenv("NEXO_TEST_WATCHER_LOCK"))
		if err != nil {
			os.Exit(2)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		<-signals
		release()
		os.Exit(0)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func setClock(s *Source, now func() time.Time) { s.now = now }

func setStateDir(s *Source, dir string) { s.stateDir = dir }

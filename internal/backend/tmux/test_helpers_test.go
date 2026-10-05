package tmux

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestMain keeps backend tests from writing container history into the
// developer's real XDG state directory, and gives every private tmux server a
// short socket directory of its own.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nexo-tmux-test-state")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_STATE_HOME", dir)
	// Tests start private tmux servers with -L. A short, private TMUX_TMPDIR
	// keeps them off the caller's servers and immune to an overlong inherited
	// socket directory; dropping TMUX keeps implicit inside-tmux detection
	// from changing what a test exercises.
	socketDir, err := os.MkdirTemp("/tmp", "nx")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("TMUX_TMPDIR", socketDir)
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
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
	_ = os.RemoveAll(socketDir)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func setClock(s *Source, now func() time.Time) { s.now = now }

func setStateDir(s *Source, dir string) { s.stateDir = dir }

// testSocket returns the private tmux server name for one test. tmux builds
// the socket path from TMUX_TMPDIR (set in TestMain) plus this name, and a
// sun_path holds only 104 bytes on macOS, so the name carries a short hash
// instead of the test name. variant separates servers within one test.
func testSocket(t *testing.T, variant string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + "\x00" + variant))
	return "nx-" + hex.EncodeToString(sum[:6])
}

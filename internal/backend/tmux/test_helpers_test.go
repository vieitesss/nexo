package tmux

import (
	"os"
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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func setClock(s *Source, now func() time.Time) { s.now = now }

func setStateDir(s *Source, dir string) { s.stateDir = dir }

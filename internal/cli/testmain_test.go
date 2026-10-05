package cli

import (
	"os"
	"testing"
)

// TestMain gives the CLI integration tests a short, private tmux socket
// directory instead of inheriting the caller's TMUX_TMPDIR, and drops TMUX so
// a run from inside a tmux pane cannot change what the tests exercise.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("/tmp", "nx")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("TMUX_TMPDIR", dir)
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

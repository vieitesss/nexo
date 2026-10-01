//go:build darwin

package tui

import (
	"os"
	"testing"
)

func openThemePTY(t *testing.T) (terminal, peer *os.File) {
	t.Helper()
	t.Skip("pty setup is unavailable on this platform")
	return nil, nil
}

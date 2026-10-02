package backend

import (
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
)

// ControllingTTY returns the path attached to standard input when available.
// A non-terminal stdin returns an empty string, which keeps detection useful
// in pipes and scripts.
func ControllingTTY() string {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 || !term.IsTerminal(os.Stdin.Fd()) {
		return ""
	}
	path, err := os.Readlink("/proc/self/fd/0")
	if err != nil || filepath.Base(path) == "0" {
		return ""
	}
	return path
}

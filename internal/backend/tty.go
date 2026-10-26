package backend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/vieitesss/nexo/internal/runner"
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

// controlTTY resolves the terminal this invocation actually controls. Standard
// input names it when it is a terminal; otherwise the kernel's session terminal
// is asked for, so a redirected stdin (a pipe, a wrapper) does not hide the
// terminal the user is typing in. An empty result means no controlling
// terminal at all, as in cron jobs and pipes.
func controlTTY(ctx context.Context, tty string, commandRunner runner.CommandRunner) string {
	if tty != "" {
		return tty
	}
	path, ok := processTTY(ctx, commandRunner, os.Getpid())
	if !ok {
		return ""
	}
	return path
}

// processTTY reports the controlling terminal of one local process. ps prints
// it without the /dev prefix on macOS ("ttys010") and Linux ("pts/4"), and
// prints "??" when the process has none.
func processTTY(ctx context.Context, commandRunner runner.CommandRunner, pid int) (string, bool) {
	if commandRunner == nil || pid <= 0 {
		return "", false
	}
	output, err := commandRunner.Run(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", false
	}
	return normalizeTTY(string(output))
}

// normalizeTTY accepts both the absolute path tmux reports and the short form
// ps prints, and rejects the "?" placeholders for "no terminal".
func normalizeTTY(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "?") {
		return "", false
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), true
	}
	return filepath.Clean(filepath.Join("/dev", value)), true
}

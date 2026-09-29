package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/runner"
)

const notificationInterval = 3 * time.Second

// Notifier sends desktop notifications from the tmux backend. It owns the
// process-wide-for-this-watcher rate limit so several panes share one budget.
type Notifier struct {
	runner   runner.CommandRunner
	now      func() time.Time
	lookPath func(string) (string, error)
	goos     string

	mu    sync.Mutex
	last  time.Time
	sound bool
}

// NewNotifier creates a tmux desktop notifier using the supplied command
// runner. The runner seam keeps notification tests from starting OS tools.
func NewNotifier(r runner.CommandRunner) *Notifier {
	return &Notifier{runner: r, now: time.Now, lookPath: exec.LookPath, goos: runtime.GOOS}
}

// ConfigureSound controls whether a short best-effort sound accompanies a
// desktop notification.
func (n *Notifier) ConfigureSound(enabled bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sound = enabled
}

// Notify implements capabilities.Notifier. Missing notification tools are
// treated as a normal no-op so watching remains useful on headless machines.
func (n *Notifier) Notify(ctx context.Context, message string) error {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.runner == nil {
		return errors.New("tmux notifier requires a command runner")
	}

	now := n.now()
	if !n.last.IsZero() && now.Sub(n.last) < notificationInterval {
		return nil
	}
	program, args, ok := notificationCommand(n.goos, message)
	if !ok || !n.toolAvailable(program) {
		return nil
	}
	if _, err := n.runner.Run(ctx, program, args...); err != nil {
		return err
	}
	n.last = now
	if n.sound {
		n.playSound(ctx)
	}
	return nil
}

func (n *Notifier) toolAvailable(program string) bool {
	if n.lookPath == nil {
		return true
	}
	_, err := n.lookPath(program)
	return err == nil
}

func (n *Notifier) playSound(ctx context.Context) {
	program, args, ok := soundCommand(n.goos)
	if !ok {
		return
	}
	if n.goos == "linux" && !n.toolAvailable(program) {
		program = "paplay"
		args = []string{"/usr/share/sounds/freedesktop/stereo/message-new-instant.oga"}
	}
	if !n.toolAvailable(program) {
		return
	}
	// Sound is supplementary; a missing audio device must not break watching.
	_, _ = n.runner.Run(ctx, program, args...)
}

func notificationCommand(goos, message string) (string, []string, bool) {
	switch goos {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", strconv.Quote(message), strconv.Quote("nexo"))
		return "osascript", []string{"-e", script}, true
	case "linux":
		return "notify-send", []string{"nexo", message}, true
	default:
		return "", nil, false
	}
}

func soundCommand(goos string) (string, []string, bool) {
	switch goos {
	case "darwin":
		return "afplay", []string{"/System/Library/Sounds/Glass.aiff"}, true
	case "linux":
		// canberra is preferred because it resolves the desktop sound theme;
		// playSound falls back to paplay for PulseAudio/PipeWire installations.
		return "canberra-gtk-play", []string{"-i", "message-new-instant"}, true
	default:
		return "", nil, false
	}
}

var _ capabilities.Notifier = (*Notifier)(nil)

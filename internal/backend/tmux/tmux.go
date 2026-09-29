// Package tmux contains the tmux backend adapter.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/runner"
)

// Backend is the tmux backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner
	Inside bool
}

// New constructs a tmux backend using the supplied command runner. The
// optional argument identifies whether nexo is running inside tmux.
func New(r runner.CommandRunner, inside ...bool) *Backend {
	inTmux := os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
	if len(inside) > 0 {
		inTmux = inside[0]
	}
	return &Backend{Runner: r, Inside: inTmux}
}

// Name identifies this backend.
func (Backend) Name() string { return "tmux" }

// Error reports a failed tmux operation and preserves tmux's stderr.
type Error struct {
	Operation string
	Args      []string
	Stderr    string
	Err       error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("tmux %s: %s", e.Operation, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("tmux %s: %v", e.Operation, e.Err)
}

// Unwrap preserves the command runner error for errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// UnsupportedError reports a project-opening option that tmux does not
// support.
type UnsupportedError struct {
	Option string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("tmux does not support %s", e.Option)
}

// OpenProject opens path in a tmux session and focuses that session.
func (b *Backend) OpenProject(ctx context.Context, path string, options capabilities.OpenOptions) error {
	if options.ContainerID != "" {
		return &UnsupportedError{Option: "--workspace-id"}
	}
	if b == nil || b.Runner == nil {
		return errors.New("tmux project opener requires a command runner")
	}

	name := sessionName(path)
	_, err := b.run(ctx, "has-session", "-t", "="+name)
	exists := err == nil
	if err != nil && !isMissingSession(err) {
		return err
	}
	if !exists {
		if _, err := b.run(ctx, "new-session", "-d", "-s", name, "-c", path); err != nil {
			return err
		}
	}

	if b.Inside {
		_, err = b.run(ctx, "switch-client", "-t", "="+name)
		return err
	}
	return b.runInteractive(ctx, "attach-session", "-t", "="+name)
}

func (b *Backend) runInteractive(ctx context.Context, operation string, args ...string) error {
	interactive, ok := b.Runner.(runner.InteractiveRunner)
	if !ok {
		return &Error{
			Operation: operation,
			Args:      append([]string(nil), args...),
			Stderr:    "interactive command runner is unavailable",
			Err:       errors.New("runner does not support interactive commands"),
		}
	}
	if err := interactive.RunInteractive(ctx, "tmux", append([]string{operation}, args...)...); err != nil {
		stderr := interactiveStderr(err)
		if stderr == "" {
			stderr = err.Error()
		}
		return &Error{
			Operation: operation,
			Args:      append([]string(nil), args...),
			Stderr:    stderr,
			Err:       err,
		}
	}
	return nil
}

func (b *Backend) run(ctx context.Context, operation string, args ...string) ([]byte, error) {
	output, err := b.Runner.Run(ctx, "tmux", append([]string{operation}, args...)...)
	if err == nil {
		return output, nil
	}
	stderr := string(output)
	if captured := interactiveStderr(err); captured != "" {
		stderr = captured
	}
	if stderr == "" {
		stderr = err.Error()
	}
	return nil, &Error{
		Operation: operation,
		Args:      append([]string(nil), args...),
		Stderr:    stderr,
		Err:       err,
	}
}

func interactiveStderr(err error) string {
	var interactiveErr *runner.InteractiveError
	if errors.As(err, &interactiveErr) && interactiveErr.Stderr != "" {
		return interactiveErr.Stderr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return string(exitErr.Stderr)
	}
	return ""
}

func isMissingSession(err error) bool {
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) {
		return false
	}
	message := strings.ToLower(tmuxErr.Stderr)
	return strings.Contains(message, "can't find session") ||
		strings.Contains(message, "session not found") ||
		strings.Contains(message, "no server running")
}

func sessionName(path string) string {
	clean := filepath.Clean(path)
	parent := filepath.Base(filepath.Dir(clean))
	base := filepath.Base(clean)
	return strings.NewReplacer(".", "_", ":", "_").Replace(parent + "_" + base)
}

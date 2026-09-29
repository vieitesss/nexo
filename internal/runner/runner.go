// Package runner provides the seam for external program calls.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// CommandRunner runs an external program and returns its standard output.
// Implementations can be replaced by fakes in tests.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// InteractiveRunner runs a command attached to the current terminal.
type InteractiveRunner interface {
	CommandRunner
	RunInteractive(context.Context, string, ...string) error
}

// InteractiveError reports a failed interactive command and preserves stderr.
type InteractiveError struct {
	Name   string
	Args   []string
	Stderr string
	Err    error
}

func (e *InteractiveError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s: %s", e.Name, e.Stderr)
	}
	return fmt.Sprintf("%s: %v", e.Name, e.Err)
}

// Unwrap preserves the process error for errors.Is and errors.As.
func (e *InteractiveError) Unwrap() error { return e.Err }

// Runner is the short name for CommandRunner.
type Runner = CommandRunner

// Func adapts a function to CommandRunner.
type Func func(context.Context, string, ...string) ([]byte, error)

// Run implements CommandRunner.
func (f Func) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

// ExecRunner runs commands using os/exec.
type ExecRunner struct{}

// Run implements CommandRunner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// RunInteractive runs a command with the current process terminal attached.
func (ExecRunner) RunInteractive(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := command.Run(); err != nil {
		return &InteractiveError{
			Name:   name,
			Args:   append([]string(nil), args...),
			Stderr: stderr.String(),
			Err:    err,
		}
	}
	return nil
}

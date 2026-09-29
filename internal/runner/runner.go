// Package runner provides the seam for external program calls.
package runner

import (
	"context"
	"os/exec"
)

// CommandRunner runs an external program and returns its standard output.
// Implementations can be replaced by fakes in tests.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

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

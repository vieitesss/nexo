// Package tmux contains the tmux backend adapter.
package tmux

import "github.com/vieitesss/nexo/internal/runner"

// Backend is the tmux backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner
}

// New constructs a tmux backend using the supplied command runner.
func New(r runner.CommandRunner) *Backend {
	return &Backend{Runner: r}
}

// Name identifies this backend.
func (Backend) Name() string { return "tmux" }

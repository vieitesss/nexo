// Package herdr contains the Herdr backend adapter.
package herdr

import "github.com/vieitesss/nexo/internal/runner"

// Backend is the Herdr backend. Capabilities are added independently as each
// feature is implemented.
type Backend struct {
	Runner runner.CommandRunner
}

// New constructs a Herdr backend using the supplied command runner.
func New(r runner.CommandRunner) *Backend {
	return &Backend{Runner: r}
}

// Name identifies this backend.
func (Backend) Name() string { return "herdr" }

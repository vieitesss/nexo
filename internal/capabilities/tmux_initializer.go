package capabilities

import "context"

// InitInput contains the tmux adapter settings for one invocation.
type InitInput struct {
	ConfigPath string
}

// TmuxInitializer installs the tmux adapter's hooks and watcher.
type TmuxInitializer interface {
	Init(context.Context, InitInput) error
}

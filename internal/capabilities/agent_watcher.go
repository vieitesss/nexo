package capabilities

import "context"

// AgentWatcher runs the backend's agent watcher until the context is done.
type AgentWatcher interface {
	WatchAgents(context.Context) error
}

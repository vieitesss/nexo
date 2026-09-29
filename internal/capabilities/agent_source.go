package capabilities

import "context"

// AgentSource provides agent panes and their state.
type AgentSource interface {
	ListAgents(context.Context) ([]Agent, error)
}

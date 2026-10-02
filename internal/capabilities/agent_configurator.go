package capabilities

import "github.com/vieitesss/nexo/internal/config"

// AgentConfigurator applies the shared agent configuration to a backend.
type AgentConfigurator interface {
	ConfigureAgents(config.AgentsConfig)
}

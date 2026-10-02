package capabilities

// AgentCommandConfigurator supplies the executable and config path used by
// agent hooks.
type AgentCommandConfigurator interface {
	ConfigureAgentCommand(string, string)
}

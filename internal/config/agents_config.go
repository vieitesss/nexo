package config

// AgentsConfig controls agent discovery and polling.
type AgentsConfig struct {
	Processes      []string          `toml:"processes"`
	IdleSeconds    int               `toml:"idle-seconds"`
	PollInterval   int               `toml:"poll-interval"`
	WorkingPattern map[string]string `toml:"working-pattern"`
}

var defaultAgentProcesses = []string{"pi", "claude", "codex", "opencode", "hermes", "aider", "cursor"}

// WithDefaults returns a copy with the documented agent defaults filled in.
func (c AgentsConfig) WithDefaults() AgentsConfig {
	if len(c.Processes) == 0 {
		c.Processes = append([]string(nil), defaultAgentProcesses...)
	}
	if c.IdleSeconds <= 0 {
		c.IdleSeconds = 3
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 2
	}
	if c.WorkingPattern == nil {
		c.WorkingPattern = map[string]string{}
	}
	return c
}

// DefaultAgentProcesses returns the built-in harness allowlist.
func DefaultAgentProcesses() []string {
	return append([]string(nil), defaultAgentProcesses...)
}

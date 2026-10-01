package config

// AgentsConfig controls agent discovery, polling, and notifications.
type AgentsConfig struct {
	Processes      []string          `toml:"processes"`
	IdleSeconds    int               `toml:"idle-seconds"`
	PollInterval   int               `toml:"poll-interval"`
	WorkingPattern map[string]string `toml:"working-pattern"`
	Notify         *bool             `toml:"notify"` // notify working-to-stopped transitions
	Sound          bool              `toml:"sound"`  // play a short notification sound
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
	if c.Notify == nil {
		enabled := true
		c.Notify = &enabled
	}
	return c
}

// NotificationsEnabled reports whether stop notifications are enabled. A nil
// value is treated as the documented default of true.
func (c AgentsConfig) NotificationsEnabled() bool {
	return c.Notify == nil || *c.Notify
}

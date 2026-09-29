package config

// TmuxConfig controls the tmux adapter's key bindings and popup geometry.
// A key set to "none" is unbound instead of installed.
type TmuxConfig struct {
	PlaceKey    string `toml:"place-key"`
	AgentKey    string `toml:"agent-key"`
	PopupWidth  string `toml:"popup-width"`
	PopupHeight string `toml:"popup-height"`
	PopupX      string `toml:"popup-x"`
	PopupY      string `toml:"popup-y"`
}

// WithDefaults returns a copy with the documented tmux defaults filled in.
func (c TmuxConfig) WithDefaults() TmuxConfig {
	if c.PlaceKey == "" {
		c.PlaceKey = "C-s"
	}
	if c.AgentKey == "" {
		c.AgentKey = "a"
	}
	if c.PopupWidth == "" {
		c.PopupWidth = "100%"
	}
	if c.PopupHeight == "" {
		c.PopupHeight = "100%"
	}
	if c.PopupX == "" {
		c.PopupX = "C"
	}
	if c.PopupY == "" {
		c.PopupY = "C"
	}
	return c
}

package config

// TmuxConfig is reserved for backend-neutral tmux settings. Key bindings and
// popup geometry are owned by the user's tmux.conf.
type TmuxConfig struct{}

type legacyTmuxConfig struct {
	PlaceKey    *string `toml:"place-key"`
	AgentKey    *string `toml:"agent-key"`
	PopupWidth  *string `toml:"popup-width"`
	PopupHeight *string `toml:"popup-height"`
	PopupX      *string `toml:"popup-x"`
	PopupY      *string `toml:"popup-y"`
}

func (c legacyTmuxConfig) config() TmuxConfig { return TmuxConfig{} }

func (c legacyTmuxConfig) present() bool {
	return c.PlaceKey != nil || c.AgentKey != nil || c.PopupWidth != nil || c.PopupHeight != nil || c.PopupX != nil || c.PopupY != nil
}

package config

import "fmt"

// TUIConfig controls the terminal interface appearance.
type TUIConfig struct {
	Theme string `toml:"theme"`
}

const (
	ThemeAuto  = "auto"
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// WithDefaults returns a copy with the documented theme default filled in.
func (c TUIConfig) WithDefaults() TUIConfig {
	if c.Theme == "" {
		c.Theme = ThemeAuto
	}
	return c
}

// EffectiveTheme returns the configured theme, treating an empty value as auto
// for programmatically constructed configurations.
func (c TUIConfig) EffectiveTheme() string {
	return c.WithDefaults().Theme
}

func validateTUI(c TUIConfig) error {
	switch c.Theme {
	case "", ThemeAuto, ThemeDark, ThemeLight:
		return nil
	default:
		return fmt.Errorf("invalid tui theme %q", c.Theme)
	}
}

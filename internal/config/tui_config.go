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

// ThemeFromConfig returns the effective [tui] theme in config bytes. The
// running TUI uses it to follow theme edits; it applies the same validation as
// a full load so a bad value can never replace a working palette.
func ThemeFromConfig(contents []byte, path string) (string, error) {
	cfg, err := parse(contents, path)
	if err != nil {
		return "", err
	}
	return cfg.TUI.EffectiveTheme(), nil
}

func validateTUI(c TUIConfig) error {
	switch c.Theme {
	case "", ThemeAuto, ThemeDark, ThemeLight:
		return nil
	default:
		return fmt.Errorf("invalid tui theme %q", c.Theme)
	}
}

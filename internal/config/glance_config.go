package config

import (
	"fmt"
	"strings"
)

// glanceFieldOrder is the canonical order of the glance row's configurable
// fields. A configured selection is rendered in this order regardless of the
// order the keys appear in the file.
var glanceFieldOrder = []string{"dot", "target", "harness", "session", "window", "branch", "age"}

// GlanceFieldOrder returns the canonical order of glance fields.
func GlanceFieldOrder() []string {
	return append([]string(nil), glanceFieldOrder...)
}

// GlanceConfig controls the tmux glance row: which agent fields it shows, how
// the state dot is drawn, and the optional row tint.
type GlanceConfig struct {
	Fields []string     `toml:"fields"`
	Tint   string       `toml:"tint"`
	Dots   string       `toml:"dots"`
	Colors GlanceColors `toml:"colors"`
}

// GlanceColors are the dot colours per agent state. Each is a tmux colour or a
// @palette option.
type GlanceColors struct {
	Working string `toml:"working"`
	Unseen  string `toml:"unseen"`
	Seen    string `toml:"seen"`
}

// Glance dot modes: one dot coloured by state, or one uncoloured glyph per
// state for terminals without colour.
const (
	GlanceDotsColor   = "color"
	GlanceDotsSymbols = "symbols"
)

// defaultGlanceFields is the smallest row that still identifies an agent and
// its state.
var defaultGlanceFields = []string{"dot", "target"}

// defaultGlanceColors follow the TUI state cues. The seen dot is green, an idle
// agent that needs nothing, and matches the TUI's green seen state: a grey such
// as brightblack vanishes on themes whose bright black is the background.
var defaultGlanceColors = GlanceColors{Working: "yellow", Unseen: "red", Seen: "green"}

// WithDefaults returns a copy with the documented glance defaults filled in.
func (c GlanceConfig) WithDefaults() GlanceConfig {
	if len(c.Fields) == 0 {
		c.Fields = append([]string(nil), defaultGlanceFields...)
	}
	c.Dots = strings.TrimSpace(c.Dots)
	if c.Dots == "" {
		c.Dots = GlanceDotsColor
	}
	if strings.TrimSpace(c.Colors.Working) == "" {
		c.Colors.Working = defaultGlanceColors.Working
	}
	if strings.TrimSpace(c.Colors.Unseen) == "" {
		c.Colors.Unseen = defaultGlanceColors.Unseen
	}
	if strings.TrimSpace(c.Colors.Seen) == "" {
		c.Colors.Seen = defaultGlanceColors.Seen
	}
	return c
}

// OrderedFields returns the enabled fields in canonical render order.
func (c GlanceConfig) OrderedFields() []string {
	c = c.WithDefaults()
	enabled := make(map[string]struct{}, len(c.Fields))
	for _, field := range c.Fields {
		enabled[strings.TrimSpace(field)] = struct{}{}
	}
	ordered := make([]string, 0, len(c.Fields))
	for _, field := range glanceFieldOrder {
		if _, ok := enabled[field]; ok {
			ordered = append(ordered, field)
		}
	}
	return ordered
}

// validateGlance rejects unusable [glance] values before any status row is
// written.
func validateGlanceFields(values []string) error {
	if len(values) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(glanceFieldOrder))
	for _, field := range glanceFieldOrder {
		known[field] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		field := strings.TrimSpace(value)
		if field == "" {
			return fmt.Errorf("empty glance field")
		}
		if _, ok := known[field]; !ok {
			return fmt.Errorf("unknown glance field %q", field)
		}
		if _, ok := seen[field]; ok {
			return fmt.Errorf("duplicate glance field %q", field)
		}
		seen[field] = struct{}{}
	}
	return nil
}

// validateGlanceTint accepts the three documented forms: an empty or
// "default" tint, a tmux colour, or a palette option such as @thm_surface_0.
func validateGlanceTint(value string) error {
	if !isGlanceColour(value) {
		return fmt.Errorf("invalid glance tint %q", value)
	}
	return nil
}

// validateGlanceDots accepts an empty value, which receives the default, or
// one of the two dot modes.
func validateGlanceDots(value string) error {
	switch strings.TrimSpace(value) {
	case "", GlanceDotsColor, GlanceDotsSymbols:
		return nil
	default:
		return fmt.Errorf("invalid glance dots %q (want %s or %s)", value, GlanceDotsColor, GlanceDotsSymbols)
	}
}

// validateGlanceColors checks every state colour and returns the key of the
// first rejected one, so the error points at the line a user has to edit.
func validateGlanceColors(colors GlanceColors) (string, error) {
	for _, entry := range []struct{ key, value string }{
		{"glance.colors.working", colors.Working},
		{"glance.colors.unseen", colors.Unseen},
		{"glance.colors.seen", colors.Seen},
	} {
		if !isGlanceColour(entry.value) {
			return entry.key, fmt.Errorf("invalid glance colour %q", entry.value)
		}
	}
	return "", nil
}

// isGlanceColour reports whether value is empty, a tmux colour (including
// "default"), or a palette option such as @thm_surface_0.
func isGlanceColour(value string) bool {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return true
	case strings.HasPrefix(value, "@"):
		return isPaletteOption(value)
	default:
		return isTmuxColour(value)
	}
}

// isPaletteOption reports whether value is a tmux user option name, the form
// a theme palette exposes for a colour.
func isPaletteOption(value string) bool {
	if len(value) < 2 || value[0] != '@' {
		return false
	}
	for _, char := range value[1:] {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '_' || char == '-':
		default:
			return false
		}
	}
	return true
}

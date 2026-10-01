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

// GlanceConfig controls the tmux glance row: which agent fields it shows and
// the optional row tint.
type GlanceConfig struct {
	Fields []string `toml:"fields"`
	Tint   string   `toml:"tint"`
}

// defaultGlanceFields is the smallest row that still identifies an agent and
// its state.
var defaultGlanceFields = []string{"dot", "target"}

// WithDefaults returns a copy with the documented glance defaults filled in.
func (c GlanceConfig) WithDefaults() GlanceConfig {
	if len(c.Fields) == 0 {
		c.Fields = append([]string(nil), defaultGlanceFields...)
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
	value = strings.TrimSpace(value)
	if value == "" || value == "default" {
		return nil
	}
	if strings.HasPrefix(value, "@") {
		if !isPaletteOption(value) {
			return fmt.Errorf("invalid glance tint %q", value)
		}
		return nil
	}
	if !isTmuxColour(value) {
		return fmt.Errorf("invalid glance tint %q", value)
	}
	return nil
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

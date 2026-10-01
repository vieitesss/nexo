package config

import (
	"fmt"
	"strconv"
	"strings"
)

// highlightColorKey names the configuration key a rejected highlight value
// belongs to, so the error points at the line a user has to edit.
const highlightColorKey = "agents.highlight-color"

// AgentsConfig controls agent discovery, polling, and notifications.
type AgentsConfig struct {
	Processes      []string          `toml:"processes"`
	IdleSeconds    int               `toml:"idle-seconds"`
	PollInterval   int               `toml:"poll-interval"`
	WorkingPattern map[string]string `toml:"working-pattern"`
	Notify         *bool             `toml:"notify"`          // notify working-to-stopped transitions
	Sound          bool              `toml:"sound"`           // play a short notification sound
	HighlightColor string            `toml:"highlight-color"` // tmux window-list colour for unseen stopped agents
}

// defaultAgentHighlightColor is the tmux colour used to highlight a window
// that holds an unseen stopped agent.
const defaultAgentHighlightColor = "red"

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
	if c.HighlightColor == "" {
		c.HighlightColor = defaultAgentHighlightColor
	}
	return c
}

// validateAgents rejects an unusable [agents] value before any watcher starts.
func validateAgents(c AgentsConfig) error {
	return validateHighlightColor(c.HighlightColor)
}

// validateHighlightColor checks the two documented forms of the highlight
// setting: a bare tmux colour, or a full comma-separated style. An empty value
// is valid and receives the default later, and it is validated here so a bad
// value fails at the config boundary instead of on the first unseen agent.
func validateHighlightColor(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.Contains(value, "=") {
		if !isTmuxColour(value) {
			return fmt.Errorf("invalid highlight colour %q", value)
		}
		return nil
	}
	return validateHighlightStyle(value)
}

// tmuxNamedColours are the symbolic colours tmux accepts for fg=, bg= and us=.
var tmuxNamedColours = map[string]struct{}{
	"default": {}, "terminal": {},
	"black": {}, "red": {}, "green": {}, "yellow": {},
	"blue": {}, "magenta": {}, "cyan": {}, "white": {},
	"brightblack": {}, "brightred": {}, "brightgreen": {}, "brightyellow": {},
	"brightblue": {}, "brightmagenta": {}, "brightcyan": {}, "brightwhite": {},
}

// tmuxStyleAttributes are the attribute names tmux accepts on their own in a
// style. Every one may also be written with a "no" prefix to turn it off.
var tmuxStyleAttributes = map[string]struct{}{
	"none": {}, "bold": {}, "bright": {}, "dim": {}, "underscore": {},
	"blink": {}, "reverse": {}, "hidden": {}, "italics": {},
	"strikethrough": {}, "double-underscore": {}, "curly-underscore": {},
	"dotted-underscore": {}, "dashed-underscore": {}, "overline": {},
	"push-default": {}, "pop-default": {},
}

// tmuxStyleProperties are the non-colour properties tmux accepts as key=value
// in a style. Their values are left to tmux to judge; only the colour
// properties carry a value nexo can validate without guessing tmux's grammar.
var tmuxStyleProperties = map[string]struct{}{
	"align": {}, "fill": {}, "range": {}, "list": {},
	"width": {}, "pad": {}, "link": {},
}

func isTmuxColour(value string) bool {
	if _, ok := tmuxNamedColours[strings.ToLower(value)]; ok {
		return true
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"colour", "color"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		index, err := strconv.Atoi(lower[len(prefix):])
		return err == nil && index >= 0 && index <= 255
	}
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, digit := range value[1:] {
		if !isHexDigit(digit) {
			return false
		}
	}
	return true
}

func isHexDigit(digit rune) bool {
	return (digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f') || (digit >= 'A' && digit <= 'F')
}

func isTmuxStyleAttribute(value string) bool {
	if _, ok := tmuxStyleAttributes[value]; ok {
		return true
	}
	base := strings.TrimPrefix(value, "no")
	if base == value {
		return false
	}
	_, ok := tmuxStyleAttributes[base]
	return ok
}

func validateHighlightStyle(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("invalid highlight style %q: empty attribute", value)
		}
		key, argument, hasArgument := strings.Cut(part, "=")
		if !hasArgument {
			if !isTmuxStyleAttribute(part) {
				return fmt.Errorf("invalid highlight style %q: unknown attribute %q", value, part)
			}
			continue
		}
		key = strings.TrimSpace(key)
		argument = strings.TrimSpace(argument)
		if argument == "" {
			return fmt.Errorf("invalid highlight style %q: %q has no value", value, part)
		}
		switch key {
		case "fg", "bg", "us":
			if !isTmuxColour(argument) {
				return fmt.Errorf("invalid highlight style %q: %q is not a tmux colour", value, argument)
			}
		default:
			if _, ok := tmuxStyleProperties[key]; !ok {
				return fmt.Errorf("invalid highlight style %q: unknown property %q", value, key)
			}
		}
	}
	return nil
}

// NotificationsEnabled reports whether stop notifications are enabled. A nil
// value is treated as the documented default of true.
func (c AgentsConfig) NotificationsEnabled() bool {
	return c.Notify == nil || *c.Notify
}

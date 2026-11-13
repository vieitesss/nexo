package config

import (
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/shell"
)

// ProjectBehavior names a way to open a project beside the current pane
// instead of in its own container. Each backend implements the ones it can.
type ProjectBehavior string

const (
	BehaviorSplitRight ProjectBehavior = "split-right"
	BehaviorSplitDown  ProjectBehavior = "split-down"
	BehaviorTab        ProjectBehavior = "tab"
	BehaviorFloat      ProjectBehavior = "float"
)

// ProjectBehaviors lists every known behavior in a stable order.
var ProjectBehaviors = []ProjectBehavior{BehaviorSplitRight, BehaviorSplitDown, BehaviorTab, BehaviorFloat}

// ProjectAction binds a TUI key to a project behavior.
type ProjectAction struct {
	Key      string          `toml:"key"`
	Behavior ProjectBehavior `toml:"behavior"`
	// Label is the footer hint; empty uses the behavior name.
	Label string `toml:"label"`
}

const (
	// The footer has room for few hints beside the built-in ones.
	maxProjectActions     = 3
	maxProjectActionLabel = 8
)

// reservedProjectActionKeys are keys the TUI or the terminal already use:
// built-in actions, the search box's default textinput editing keys (a, b,
// f, k, v, alt+b/d/f), and terminal aliases for Tab (i), Enter (m),
// newline (j), and Backspace (h).
var reservedProjectActionKeys = map[string]bool{
	"ctrl+a": true, "ctrl+b": true, "ctrl+c": true, "ctrl+d": true, "ctrl+e": true,
	"ctrl+f": true, "ctrl+h": true, "ctrl+i": true, "ctrl+j": true, "ctrl+k": true,
	"ctrl+m": true, "ctrl+n": true, "ctrl+o": true, "ctrl+p": true, "ctrl+r": true,
	"ctrl+t": true, "ctrl+u": true, "ctrl+v": true, "ctrl+w": true, "ctrl+x": true,
	"ctrl+y": true, "alt+b": true, "alt+d": true, "alt+f": true,
}

// DisplayLabel returns the footer hint text for the action.
func (a ProjectAction) DisplayLabel() string {
	if a.Label != "" {
		return a.Label
	}
	return string(a.Behavior)
}

// DisplayKey renders the key in the footer's spelling: ctrl+f -> Ctrl-F.
func (a ProjectAction) DisplayKey() string {
	modifier, letter, _ := strings.Cut(a.Key, "+")
	return strings.ToUpper(modifier[:1]) + modifier[1:] + "-" + strings.ToUpper(letter)
}

func validateProjectActions(actions []ProjectAction) error {
	if len(actions) > maxProjectActions {
		return fmt.Errorf("at most %d project actions are allowed, got %d", maxProjectActions, len(actions))
	}
	seen := map[string]bool{}
	for i, action := range actions {
		if err := validateProjectActionKey(action.Key); err != nil {
			return fmt.Errorf("project action %d: %w", i+1, err)
		}
		if seen[action.Key] {
			return fmt.Errorf("project action %d: key %q is bound twice", i+1, action.Key)
		}
		seen[action.Key] = true
		if !knownProjectBehavior(action.Behavior) {
			return fmt.Errorf("project action %d: unknown behavior %q", i+1, action.Behavior)
		}
		if shell.StringWidth(action.Label) > maxProjectActionLabel {
			return fmt.Errorf("project action %d: label %q is wider than %d columns", i+1, action.Label, maxProjectActionLabel)
		}
	}
	return nil
}

// validateProjectActionKey accepts ctrl+<letter> or alt+<letter>, lower case,
// as Bubble Tea spells them. Plain letters are search input.
func validateProjectActionKey(key string) error {
	modifier, letter, ok := strings.Cut(key, "+")
	if !ok || (modifier != "ctrl" && modifier != "alt") || len(letter) != 1 || letter[0] < 'a' || letter[0] > 'z' {
		return fmt.Errorf("key %q must be ctrl+<letter> or alt+<letter>", key)
	}
	if reservedProjectActionKeys[key] {
		return fmt.Errorf("key %q is already used by nexo", key)
	}
	return nil
}

func knownProjectBehavior(behavior ProjectBehavior) bool {
	for _, known := range ProjectBehaviors {
		if behavior == known {
			return true
		}
	}
	return false
}

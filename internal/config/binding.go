package config

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateBindingKey validates a tmux key token. Backend-specific callers use
// ValidateBindingKeyForBackend when they need a native Herdr spelling.
func ValidateBindingKey(key string) error {
	return ValidateBindingKeyForBackend("tmux", key)
}

// ValidateBindingKeyForBackend validates one binding for the selected backend.
// Tmux key syntax is deliberately strict: modifiers may precede one printable
// character or one documented tmux key name. The Ctrl-Space prefix is rejected
// in every tmux spelling, including ^@ and C-@.
func ValidateBindingKeyForBackend(backend, key string) error {
	if key == "" {
		return nil
	}
	if strings.TrimSpace(key) != key || strings.ContainsAny(key, " \t\r\n;|&$'\"`") {
		return fmt.Errorf("binding key must be one token without whitespace or separators")
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return fmt.Errorf("binding key must not contain control characters")
		}
	}
	if !strings.EqualFold(strings.TrimSpace(backend), "tmux") {
		return validateNativeBindingKey(key)
	}
	if _, reserved, err := parseTmuxBindingKey(key); err != nil {
		return err
	} else if reserved {
		return fmt.Errorf("Ctrl-Space is reserved; choose another tmux binding key (for example C-s or a)")
	}
	return nil
}

func validateNativeBindingKey(key string) error {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "+", "", "_", "", " ", "").Replace(key))
	switch normalized {
	case "cspace", "ctrlspace", "controlspace", "c@", "ctrl@", "control@", "^@", "^space":
		return fmt.Errorf("Ctrl-Space is reserved; choose another binding key")
	}
	return nil
}

type tmuxBindingKey struct {
	base  string
	ctrl  bool
	caret bool
}

// parseTmuxBindingKey returns a canonical representation for validation. The
// canonical form is intentionally private: setup preserves the spelling the
// user entered, but equivalent forms still share the reserved-key check.
func parseTmuxBindingKey(input string) (tmuxBindingKey, bool, error) {
	remaining := input
	parsed := tmuxBindingKey{}
	for len(remaining) > 0 {
		switch {
		case strings.HasPrefix(strings.ToUpper(remaining), "C-"):
			parsed.ctrl = true
			remaining = remaining[2:]
		case strings.HasPrefix(strings.ToUpper(remaining), "M-"):
			remaining = remaining[2:]
		case strings.HasPrefix(strings.ToUpper(remaining), "S-"):
			remaining = remaining[2:]
		case strings.HasPrefix(remaining, "^"):
			parsed.caret = true
			remaining = remaining[1:]
		default:
			goto parsedModifiers
		}
	}
parsedModifiers:
	if remaining == "" {
		return tmuxBindingKey{}, false, invalidTmuxKey(input)
	}
	canonical := strings.ToLower(remaining)
	if !tmuxKeyNames[canonical] {
		runes := []rune(remaining)
		if len(runes) != 1 || unicode.IsSpace(runes[0]) || unicode.IsControl(runes[0]) {
			return tmuxBindingKey{}, false, invalidTmuxKey(input)
		}
	}
	parsed.base = canonical
	reserved := (parsed.ctrl || parsed.caret) && (canonical == "space" || canonical == "@")
	return parsed, reserved, nil
}

func invalidTmuxKey(key string) error {
	return fmt.Errorf("invalid tmux binding key %q; use C-s, a, F1, Enter, or leave it blank", key)
}

// Names accepted by tmux's key parser. Keep this list explicit so an invalid
// form cannot reach a live tmux server merely because it is one token.
var tmuxKeyNames = map[string]bool{
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "insert": true, "delete": true,
	"pageup": true, "pagedown": true, "ppage": true, "npage": true,
	"ic": true, "dc": true, "enter": true, "return": true,
	"escape": true, "esc": true, "tab": true, "btab": true,
	"space": true, "backspace": true, "bspace": true,
	"clear": true, "cancel": true, "print": true, "pause": true,
	"f1": true, "f2": true, "f3": true, "f4": true, "f5": true,
	"f6": true, "f7": true, "f8": true, "f9": true, "f10": true,
	"f11": true, "f12": true,
	"kp0": true, "kp1": true, "kp2": true, "kp3": true, "kp4": true,
	"kp5": true, "kp6": true, "kp7": true, "kp8": true, "kp9": true,
	"kp.": true, "kp/": true, "kp*": true, "kp-": true,
	"kp+": true, "kpenter": true, "kp=": true,
}

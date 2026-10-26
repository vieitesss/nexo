package config

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateBindingKey validates a tmux key token. Backend-specific callers use
// ValidateBindingKeyForBackend when they need a native Herdr spelling.
func ValidateBindingKey(key string) error {
	return ValidateBindingKeyForBackend("tmux", key)
}

// TmuxPrefixMarker starts a tmux binding that fires after the prefix key. It is
// the same spelling Herdr uses natively, so both backends read alike.
const TmuxPrefixMarker = "prefix+"

// SplitTmuxBinding returns the tmux key name and whether it is bound in the
// prefix table (prefix+a) rather than the root table (C-s).
func SplitTmuxBinding(key string) (string, bool) {
	if len(key) > len(TmuxPrefixMarker) && strings.EqualFold(key[:len(TmuxPrefixMarker)], TmuxPrefixMarker) {
		return key[len(TmuxPrefixMarker):], true
	}
	return key, false
}

// ValidateBindingKeyForBackend validates one binding for the selected backend.
// Tmux key syntax is deliberately strict: modifiers may precede one printable
// character or one documented tmux key name, optionally after prefix+. A key
// without the prefix needs Ctrl or Meta (or is F1-F12), so it cannot steal
// ordinary typing from every pane. The Ctrl-Space prefix is rejected in every
// tmux spelling, including ^@ and C-@.
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
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "tmux":
		// The tmux parser below.
	case "rex":
		return validateRexBindingKey(key)
	default:
		return validateNativeBindingKey(key)
	}
	tmuxKey, afterPrefix := SplitTmuxBinding(key)
	parsed, reserved, err := parseTmuxBindingKey(tmuxKey)
	if err != nil {
		return err
	}
	if reserved {
		return fmt.Errorf("Ctrl-Space is reserved; choose another tmux binding key (for example C-s or prefix+a)")
	}
	if !afterPrefix && !parsed.ctrl && !parsed.caret && !parsed.meta && !isTmuxFunctionKey(parsed.base) {
		return fmt.Errorf("%s without the prefix would take that key from every pane; use prefix+%s or add C- or M-", key, key)
	}
	return nil
}

func isTmuxFunctionKey(base string) bool {
	return len(base) > 1 && base[0] == 'f' && tmuxKeyNames[base]
}

// herdrModifiers are the modifier names Herdr accepts before a key.
var herdrModifiers = map[string]bool{"ctrl": true, "control": true, "shift": true, "alt": true, "option": true, "meta": true, "cmd": true, "super": true}

// herdrNamedKeys are the key names Herdr accepts, split by whether they type a
// character. A typing key needs the prefix or a modifier other than shift.
var herdrNamedKeys = map[string]bool{
	"enter": false, "return": false, "tab": false, "esc": false, "escape": false, "backspace": false,
	"up": false, "down": false, "left": false, "right": false,
	"space": true, "minus": true, "plus": true, "period": true, "comma": true, "slash": true,
	"backslash": true, "colon": true, "semicolon": true,
}

// validateNativeBindingKey checks the shape of a Herdr key, as `herdr config
// check` does: an optional prefix+, then modifiers joined by +, then one
// character, a key name, or a function key. Herdr disables a key it cannot
// parse, so setup rejects it here instead of writing a binding that never
// fires.
func validateNativeBindingKey(key string) error {
	rest, afterPrefix := strings.CutPrefix(key, TmuxPrefixMarker)
	parts := strings.Split(rest, "+")
	if strings.HasSuffix(rest, "++") || rest == "+" {
		// "+" is itself a key, as in prefix++.
		parts = append(parts[:len(parts)-2], "+")
	}
	base := parts[len(parts)-1]
	ctrl, ctrlOrAlt := false, false
	for _, modifier := range parts[:len(parts)-1] {
		if !herdrModifiers[strings.ToLower(modifier)] {
			return fmt.Errorf("%q is not a Herdr modifier in %s; use ctrl, alt, shift, cmd or super, and put prefix+ first", modifier, key)
		}
		ctrlOrAlt = ctrlOrAlt || !strings.EqualFold(modifier, "shift")
		ctrl = ctrl || strings.EqualFold(modifier, "ctrl") || strings.EqualFold(modifier, "control")
	}
	typing, named := herdrNamedKeys[strings.ToLower(base)]
	switch {
	case named:
	case utf8.RuneCountInString(base) == 1 && !unicode.IsSpace([]rune(base)[0]):
		typing = true
	case isFunctionKey(base):
	default:
		return fmt.Errorf("%s is not a Herdr key; write it like ctrl+s, alt+g, f5 or prefix+a", key)
	}
	if ctrl && (strings.EqualFold(base, "space") || base == "@") {
		return fmt.Errorf("Ctrl-Space is reserved; choose another binding key")
	}
	if typing && !afterPrefix && !ctrlOrAlt {
		return fmt.Errorf("%s without the prefix would take that key from typing; use prefix+%s or add ctrl+ or alt+", key, key)
	}
	return nil
}

func isFunctionKey(key string) bool {
	digits, ok := strings.CutPrefix(strings.ToLower(key), "f")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// rexModifiers are the modifier names Rex accepts before a key.
var rexModifiers = map[string]bool{"ctrl": true, "control": true, "shift": true, "alt": true, "option": true, "meta": true, "cmd": true, "super": true}

// rexNamedKeys are the non-character key names `rex config check` accepts.
// Rex spells punctuation as the character itself, not as a name.
var rexNamedKeys = map[string]bool{
	"enter": true, "tab": true, "esc": true, "escape": true, "backspace": true,
	"up": true, "down": true, "left": true, "right": true, "home": true, "end": true,
	"insert": true, "delete": true, "pageup": true, "pagedown": true,
	"space": true, "plus": true,
}

// rexChord is one step of a Rex key sequence: modifiers plus one key.
type rexChord struct {
	base     string
	ctrl     bool
	modified bool
	typing   bool
}

// validateRexBindingKey validates Rex's own key grammar: chords joined by ">",
// with modifiers joined by "+". prefix+ is a Herdr spelling and is not an
// alias here. Ctrl-Space is allowed only as the leading chord of a sequence,
// which is how Rex prefix bindings are written; as the whole binding or after
// another chord it is reserved. A first chord that would type is rejected, so
// setup cannot steal an ordinary key from the terminal.
func validateRexBindingKey(key string) error {
	if strings.Contains(strings.ToLower(key), TmuxPrefixMarker) {
		return fmt.Errorf("%s uses prefix+, which is not a Rex key; write the Rex sequence like ctrl+space>a", key)
	}
	steps := strings.Split(key, ">")
	chords := make([]rexChord, len(steps))
	for index, step := range steps {
		if step == "" {
			return fmt.Errorf("%q is not a Rex key sequence; write it like ctrl+space>a", key)
		}
		chord, err := parseRexChord(step)
		if err != nil {
			return err
		}
		if chord.ctrl && (chord.base == "space" || chord.base == "@") && (len(steps) == 1 || index > 0) {
			return fmt.Errorf("Ctrl-Space is reserved; use it as the start of a sequence (ctrl+space>a) or choose another Rex binding key")
		}
		chords[index] = chord
	}
	first := chords[0]
	if first.typing && !first.modified {
		return fmt.Errorf("%s without a sequence or modifier would take that key from typing; use ctrl+space>%s or add ctrl+ or alt+", key, key)
	}
	return nil
}

func parseRexChord(input string) (rexChord, error) {
	parts := strings.Split(input, "+")
	if strings.HasSuffix(input, "++") || input == "+" {
		// "+" is itself a key, as in ctrl++ or ctrl+space>+.
		parts = append(parts[:len(parts)-2], "+")
	}
	base := parts[len(parts)-1]
	chord := rexChord{base: strings.ToLower(base)}
	for _, modifier := range parts[:len(parts)-1] {
		name := strings.ToLower(modifier)
		if !rexModifiers[name] {
			return chord, fmt.Errorf("%q is not a Rex modifier in %s; use ctrl, alt, shift, cmd or super", modifier, input)
		}
		chord.modified = chord.modified || name != "shift"
		chord.ctrl = chord.ctrl || name == "ctrl" || name == "control"
	}
	switch {
	case rexNamedKeys[chord.base]:
		chord.typing = chord.base == "space" || chord.base == "plus"
	case utf8.RuneCountInString(base) == 1 && !unicode.IsSpace([]rune(base)[0]):
		chord.typing = true
	case isFunctionKey(base):
	default:
		return chord, fmt.Errorf("%s is not a Rex key; write a sequence like ctrl+space>o or a named key like escape", input)
	}
	return chord, nil
}

type tmuxBindingKey struct {
	base  string
	ctrl  bool
	caret bool
	meta  bool
}

// parseTmuxBindingKey returns a canonical representation for validation. The
// canonical form is intentionally private: setup preserves the spelling the
// user entered, but equivalent forms still share the reserved-key check.
func parseTmuxBindingKey(input string) (tmuxBindingKey, bool, error) {
	remaining := input
	parsed := tmuxBindingKey{}
	// tmux accepts the caret form only once, as the first character.
	if strings.HasPrefix(remaining, "^") {
		parsed.caret = true
		remaining = remaining[1:]
	}
	for len(remaining) > 0 {
		switch {
		case strings.HasPrefix(strings.ToUpper(remaining), "C-"):
			parsed.ctrl = true
			remaining = remaining[2:]
		case strings.HasPrefix(strings.ToUpper(remaining), "M-"):
			parsed.meta = true
			remaining = remaining[2:]
		case strings.HasPrefix(strings.ToUpper(remaining), "S-"):
			remaining = remaining[2:]
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
	return fmt.Errorf("invalid tmux binding key %q; use C-s, M-g, F1, prefix+a, or leave it blank", key)
}

// Names accepted by tmux 3.3a's key parser. Keep this list explicit so an invalid
// form cannot reach a live tmux server merely because it is one token.
var tmuxKeyNames = map[string]bool{
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "insert": true, "delete": true,
	"pageup": true, "pagedown": true, "ppage": true, "npage": true,
	"ic": true, "dc": true, "enter": true, "escape": true,
	"tab": true, "btab": true, "space": true, "bspace": true,
	"f1": true, "f2": true, "f3": true, "f4": true, "f5": true,
	"f6": true, "f7": true, "f8": true, "f9": true, "f10": true,
	"f11": true, "f12": true,
	"kp0": true, "kp1": true, "kp2": true, "kp3": true, "kp4": true,
	"kp5": true, "kp6": true, "kp7": true, "kp8": true, "kp9": true,
	"kp.": true, "kp/": true, "kp*": true, "kp-": true,
	"kp+": true, "kpenter": true,
}

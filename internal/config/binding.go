package config

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateBindingKey accepts one tmux/Herdr key token. It deliberately rejects
// shell/config separators and every spelling of Ctrl-Space, which is reserved
// as the user's multiplexer prefix and must never be suggested by setup.
func ValidateBindingKey(key string) error {
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
	normalized := strings.ToLower(strings.NewReplacer("-", "", "+", "", "_", "", " ", "").Replace(key))
	switch normalized {
	case "cspace", "ctrlspace", "controlspace", "c@", "ctrl@", "control@":
		return fmt.Errorf("Ctrl-Space is reserved; choose another binding key")
	}
	return nil
}

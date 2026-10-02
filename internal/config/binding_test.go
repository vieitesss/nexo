package config

import "testing"

func TestValidateBindingKeyRejectsCtrlSpaceSpellingsAndInjection(t *testing.T) {
	for _, key := range []string{"C-Space", "ctrl+space", "control_space", " C-s ", "C-@", "ctrl+@", "C s", "a;run-shell", "a\nrun-shell"} {
		if err := ValidateBindingKey(key); err == nil {
			t.Errorf("ValidateBindingKey(%q) accepted unsafe key", key)
		}
	}
	for _, key := range []string{"C-s", "a", "M-g", "F12", "space"} {
		if err := ValidateBindingKey(key); err != nil {
			t.Errorf("ValidateBindingKey(%q) rejected valid token: %v", key, err)
		}
	}
}

func TestValidateBindingKeyForTmuxUsesSupportedNames(t *testing.T) {
	for _, key := range []string{"C-s", "M-g", "S-a", "^A", "F1", "F12", "Enter", "Escape", "Tab", "Space", "BSpace", "Up", "PageUp", "PPage", "PageDown", "NPage", "IC", "DC"} {
		if err := ValidateBindingKeyForBackend("tmux", key); err != nil {
			t.Errorf("tmux key %q rejected: %v", key, err)
		}
	}
	for _, key := range []string{"garbagekey", "C-Space", "^@", "C-@", "^Space", "C-", "M-"} {
		if err := ValidateBindingKeyForBackend("tmux", key); err == nil {
			t.Errorf("tmux key %q accepted", key)
		}
	}
}

package config

import (
	"strings"
	"testing"
)

func TestValidateBindingKeyRejectsCtrlSpaceSpellingsAndInjection(t *testing.T) {
	for _, key := range []string{"C-Space", "ctrl+space", "control_space", " C-s ", "C-@", "ctrl+@", "C s", "a;run-shell", "a\nrun-shell"} {
		if err := ValidateBindingKey(key); err == nil {
			t.Errorf("ValidateBindingKey(%q) accepted unsafe key", key)
		}
	}
	for _, key := range []string{"C-s", "prefix+a", "M-g", "F12", "prefix+space"} {
		if err := ValidateBindingKey(key); err != nil {
			t.Errorf("ValidateBindingKey(%q) rejected valid token: %v", key, err)
		}
	}
}

func TestValidateBindingKeyForTmuxUsesSupportedNames(t *testing.T) {
	for _, key := range []string{"C-s", "M-g", "^A", "F1", "F12", "^C-a", "^M-a", "M-C-S-a", "C-Enter", "M-Up", "prefix+a", "prefix+S-a", "prefix+Enter", "prefix+Escape", "prefix+Tab", "prefix+Space", "prefix+BSpace", "prefix+Up", "prefix+PageUp", "prefix+PPage", "prefix+PageDown", "prefix+NPage", "prefix+IC", "prefix+DC", "prefix+KPEnter", "prefix+C-s", "Prefix+a"} {
		if err := ValidateBindingKeyForBackend("tmux", key); err != nil {
			t.Errorf("tmux key %q rejected: %v", key, err)
		}
	}
	for _, key := range []string{"garbagekey", "C-Space", "^@", "C-@", "^Space", "C-", "M-", "Return", "Esc", "BackSpace", "Clear", "Cancel", "Print", "Pause", "KP=", "^^a", "C-^a", "M-^a", "prefix+", "prefix+C-Space", "prefix+Return"} {
		if err := ValidateBindingKeyForBackend("tmux", key); err == nil {
			t.Errorf("tmux key %q accepted", key)
		}
	}
}

// The accepted and rejected spellings match what `herdr config check` (0.9.0)
// reports for a [[keys.command]] key, except Ctrl-Space: Herdr accepts it, and
// nexo rejects it as the shared multiplexer prefix.
func TestValidateBindingKeyForHerdrMatchesHerdrConfigCheck(t *testing.T) {
	for _, key := range []string{"ctrl+s", "Ctrl+S", "control+s", "ctrl+shift+s", "alt+g", "cmd+k", "super+k", "meta+k", "option+k", "ctrl+alt+shift+x", "ctrl+1", "alt+1", "ctrl+é", "ctrl+minus", "ctrl+up", "ctrl+tab", "shift+tab", "shift+enter", "f5", "F5", "shift+f5", "enter", "return", "esc", "escape", "tab", "backspace", "up", "left", "prefix+a", "prefix+A", "prefix+1", "prefix+?", "prefix+shift+r", "prefix+alt+g", "prefix+ctrl+x", "prefix+f5", "prefix+minus", "prefix+space", "prefix+enter", "prefix++", "prefix+="} {
		if err := ValidateBindingKeyForBackend("herdr", key); err != nil {
			t.Errorf("herdr key %q rejected: %v", key, err)
		}
	}
	for _, key := range []string{"C-s", "prefix+aprefix+a", "foo", "ctrl+", "prefix+", "ctrl+s+a", "ctrl-s", "Prefix+a", "prefix+prefix+a", "home", "prefix+delete", "prefix+pageup", "prefix+backtab", "prefix+equal", "ctrl+space", "ctrl+@", "prefix+ctrl+space"} {
		if err := ValidateBindingKeyForBackend("herdr", key); err == nil {
			t.Errorf("herdr key %q accepted", key)
		}
	}
}

func TestValidateBindingKeyForHerdrKeepsTypingKeysBehindThePrefix(t *testing.T) {
	for _, key := range []string{"a", "A", "shift+a", "1", "?", "space", "plus", "+"} {
		err := ValidateBindingKeyForBackend("herdr", key)
		if err == nil || !strings.Contains(err.Error(), "prefix+"+key) {
			t.Errorf("herdr direct key %q error = %v, want a prefix+%s suggestion", key, err, key)
		}
	}
}

func TestValidateBindingKeyForTmuxKeepsBareKeysBehindThePrefix(t *testing.T) {
	for _, key := range []string{"a", "S-a", "Enter", "Space", "Up", "KPEnter"} {
		err := ValidateBindingKeyForBackend("tmux", key)
		if err == nil || !strings.Contains(err.Error(), "prefix+"+key) {
			t.Errorf("tmux root key %q error = %v, want a prefix+%s suggestion", key, err, key)
		}
	}
}

func TestSplitTmuxBinding(t *testing.T) {
	for input, want := range map[string]struct {
		key         string
		afterPrefix bool
	}{
		"C-s":      {"C-s", false},
		"prefix+a": {"a", true},
		"PREFIX+a": {"a", true},
		"prefix+":  {"prefix+", false},
		"M-+":      {"M-+", false},
	} {
		key, afterPrefix := SplitTmuxBinding(input)
		if key != want.key || afterPrefix != want.afterPrefix {
			t.Errorf("SplitTmuxBinding(%q) = %q, %v; want %q, %v", input, key, afterPrefix, want.key, want.afterPrefix)
		}
	}
}

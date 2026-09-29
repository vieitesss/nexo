package tmux

import "testing"

func TestMatchesWorkingIndicator(t *testing.T) {
	tests := []struct {
		name       string
		harness    string
		screen     string
		configured map[string]string
		want       bool
	}{
		{name: "pi spinner", harness: "pi", screen: "planning ⠋", want: true},
		{name: "codex spinner", harness: "codex", screen: "⠴ working", want: true},
		{name: "claude live status", harness: "claude", screen: "⠋ Working… (esc to interrupt)", want: true},
		{name: "claude completed summary", harness: "claude", screen: "Worked for 4s (3 tool calls)", want: false},
		{name: "opencode progress", harness: "opencode", screen: "▰▰▰▱", want: true},
		{name: "hermes prompt hint", harness: "hermes", screen: "msg=interrupt", want: true},
		{name: "aider configured", harness: "aider", screen: "…", configured: map[string]string{"aider": "…"}, want: true},
		{name: "aider without default", harness: "aider", screen: "…", want: false},
		{name: "quoted pi transcript", harness: "pi", screen: `assistant said "⠋"`, want: false},
		{name: "quoted claude transcript", harness: "claude", screen: `"⠋ Working… (esc to interrupt)"`, want: false},
		{name: "quoted opencode transcript", harness: "opencode", screen: `"▰▰▰▱"`, want: false},
		{name: "quoted hermes transcript", harness: "hermes", screen: `"msg=interrupt"`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MatchesWorkingIndicator(test.harness, test.screen, test.configured); got != test.want {
				t.Fatalf("MatchesWorkingIndicator() = %v, want %v", got, test.want)
			}
		})
	}
}

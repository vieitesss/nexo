package agentdetect

import "testing"

func TestHarnessMatchesProcessCommandLine(t *testing.T) {
	allow := AllowList([]string{"pi", "claude"})
	for _, test := range []struct {
		name string
		args string
		want string
	}{
		{name: "node script", args: "node /opt/homebrew/bin/pi", want: "pi"},
		{name: "node flags", args: "node --no-warnings /x/pi", want: "pi"},
		{name: "claude resume", args: "/Users/x/.local/bin/claude --resume", want: "claude"},
		{name: "vim pi", args: "vim pi", want: ""},
		{name: "bash command string", args: `bash -c "claude --resume"`, want: ""},
		{name: "markdown file", args: "vim notes/claude.md", want: ""},
		{name: "bare interpreter", args: "node", want: ""},
		{name: "login shell", args: "-zsh", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Harness(test.args, allow); got != test.want {
				t.Fatalf("Harness(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestHarnessOfResolvesAnInterpreterScript(t *testing.T) {
	allow := AllowList([]string{"/opt/homebrew/bin/claude", "PI"})
	for _, test := range []struct {
		name       string
		executable string
		script     string
		want       string
	}{
		{name: "direct executable", executable: "/Users/x/.local/bin/claude", want: "claude"},
		{name: "interpreter script", executable: "node", script: "/opt/homebrew/bin/pi", want: "pi"},
		{name: "interpreter without script", executable: "python3", want: ""},
		{name: "unconfigured program", executable: "vim", script: "/x/claude", want: ""},
		{name: "empty process", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := HarnessOf(test.executable, test.script, allow); got != test.want {
				t.Fatalf("HarnessOf(%q, %q) = %q, want %q", test.executable, test.script, got, test.want)
			}
		})
	}
}

func TestAllowListNormalizesConfiguredNames(t *testing.T) {
	allow := AllowList([]string{" /opt/homebrew/bin/Pi ", "claude", "", "   "})
	want := map[string]struct{}{"pi": {}, "claude": {}}
	if len(allow) != len(want) {
		t.Fatalf("AllowList() = %#v, want %#v", allow, want)
	}
	for name := range want {
		if _, ok := allow[name]; !ok {
			t.Fatalf("AllowList() = %#v, want %q", allow, name)
		}
	}
}

package proxy

import "testing"

func TestAttachCommandUsesEnvironmentForHerdrProxy(t *testing.T) {
	got, err := AttachCommand("/usr/bin/nexo", "herdr", "/tmp/nexo config.toml", "", "", true)
	if err != nil {
		t.Fatalf("AttachCommand() error = %v", err)
	}
	want := "exec '/usr/bin/nexo' --backend herdr --config-file '/tmp/nexo config.toml' remote attach --from-env"
	if got != want {
		t.Fatalf("AttachCommand() = %q, want %q", got, want)
	}
}

func TestAttachCommandRejectsControlAndFormatCharacters(t *testing.T) {
	for _, value := range []string{"/tmp/nexo\u0015", "/tmp/nexo\u200e"} {
		if _, err := AttachCommand(value, "herdr", "", "", "", true); err == nil {
			t.Fatalf("AttachCommand(%q) accepted a control/format character", value)
		}
	}
}

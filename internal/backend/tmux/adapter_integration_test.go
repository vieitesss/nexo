package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTmuxAdapterIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}

	server := fmt.Sprintf("nexo-test-%d", time.Now().UnixNano())
	runTmux := func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", commandArgs...).CombinedOutput()
	}
	if output, err := runTmux("new-session", "-d", "-s", "work", "sleep", "30"); err != nil {
		t.Fatalf("create throwaway tmux server: %v\n%s", err, output)
	}
	defer func() { _, _ = runTmux("kill-server") }()

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "nexo")
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/nexo")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build nexo: %v\n%s", err, output)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[tmux]\nplace-key = \"C-x\"\nagent-key = \"b\"\npopup-width = \"70%\"\npopup-height = \"60%\"\npopup-x = \"C\"\npopup-y = \"C\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf("%s --backend=tmux --config-file=%s tmux init", binary, configPath)
	if output, err := runTmux("run-shell", command); err != nil {
		t.Fatalf("run adapter: %v\n%s", err, output)
	}

	var rootKey, prefixKey, focusHook, changedHook []byte
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		rootKey, _ = runTmux("list-keys", "-T", "root", "C-x")
		prefixKey, _ = runTmux("list-keys", "-T", "prefix", "b")
		focusHook, _ = runTmux("show-hooks", "-g", "pane-focus-in")
		changedHook, _ = runTmux("show-hooks", "-g", "client-session-changed")
		if len(rootKey) > 0 && len(prefixKey) > 0 && len(focusHook) > 0 && len(changedHook) > 0 {
			break
		}
	}
	for name, output := range map[string][]byte{"root key": rootKey, "prefix key": prefixKey, "pane hook": focusHook, "session hook": changedHook} {
		if len(output) == 0 {
			t.Fatalf("%s was not installed", name)
		}
	}
	for name, output := range map[string][]byte{"root key": rootKey, "prefix key": prefixKey} {
		text := string(output)
		if !strings.Contains(text, "display-popup") || !strings.Contains(text, "70%") || !strings.Contains(text, "60%") || !strings.Contains(text, "nexo") {
			t.Fatalf("%s = %q, want configured nexo popup binding", name, text)
		}
	}
	for name, output := range map[string][]byte{"pane hook": focusHook, "session hook": changedHook} {
		if !strings.Contains(string(output), "nexo --backend tmux agents seen #{pane_id}") {
			t.Fatalf("%s = %q, want nexo focus hook", name, output)
		}
	}

	// A second install exercises ownership persistence: a user binding at the
	// new place key is preserved, while the old nexo bindings are removed.
	if output, err := runTmux("bind-key", "-n", "C-z", "display-message", "user binding"); err != nil {
		t.Fatalf("install user binding: %v\n%s", err, output)
	}
	if err := os.WriteFile(configPath, []byte("[tmux]\nplace-key = \"C-z\"\nagent-key = \"M-a\"\npopup-width = \"70%\"\npopup-height = \"60%\"\npopup-x = \"C\"\npopup-y = \"C\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runTmux("run-shell", command); err != nil {
		t.Fatalf("re-run adapter: %v\n%s", err, output)
	}
	time.Sleep(300 * time.Millisecond)
	oldPlace, _ := runTmux("list-keys", "-T", "root", "C-x")
	oldAgent, _ := runTmux("list-keys", "-T", "prefix", "b")
	userPlace, _ := runTmux("list-keys", "-T", "root", "C-z")
	newAgent, _ := runTmux("list-keys", "-T", "prefix", "M-a")
	if !strings.Contains(string(oldPlace), "unknown key") || !strings.Contains(string(oldAgent), "unknown key") {
		t.Fatalf("old nexo bindings remain: place=%q agent=%q", oldPlace, oldAgent)
	}
	if !strings.Contains(string(userPlace), "user binding") {
		t.Fatalf("user place binding = %q, want preserved binding", userPlace)
	}
	if !strings.Contains(string(newAgent), "NEXO_AGENT_NAVIGATOR_INLINE=1") {
		t.Fatalf("new agent binding = %q, want nexo binding", newAgent)
	}
}

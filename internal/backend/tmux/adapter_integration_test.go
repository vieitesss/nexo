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

	writeConfig := func(placeKey, agentKey string) {
		t.Helper()
		contents := fmt.Sprintf("[tmux]\nplace-key = %q\nagent-key = %q\npopup-width = \"70%%\"\npopup-height = \"60%%\"\npopup-x = \"C\"\npopup-y = \"C\"\n", placeKey, agentKey)
		if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := runTmux("run-shell", command); err != nil {
			t.Fatalf("re-run adapter: %v\n%s", err, output)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Re-running with a user replacement at the remembered key must not
	// overwrite it (same key, with a user rebinding).
	writeConfig("C-z", "M-a")
	userPlace, _ = runTmux("list-keys", "-T", "root", "C-z")
	if !strings.Contains(string(userPlace), "user binding") {
		t.Fatalf("same-key user binding = %q, want preserved binding", userPlace)
	}

	// A changed key leaves a user replacement at the old key untouched.
	writeConfig("C-w", "M-a")
	oldUserPlace, _ := runTmux("list-keys", "-T", "root", "C-z")
	newPlace, _ := runTmux("list-keys", "-T", "root", "C-w")
	if !strings.Contains(string(oldUserPlace), "user binding") || !strings.Contains(string(newPlace), "NEXO_PLACE_PICKER_INLINE=1") {
		t.Fatalf("changed-key bindings: old=%q new=%q", oldUserPlace, newPlace)
	}

	// A same-key re-run with no user replacement is allowed to refresh nexo.
	writeConfig("C-w", "M-a")
	newPlace, _ = runTmux("list-keys", "-T", "root", "C-w")
	if !strings.Contains(string(newPlace), "NEXO_PLACE_PICKER_INLINE=1") {
		t.Fatalf("same-key nexo binding = %q, want nexo binding", newPlace)
	}

	// A changed key after a user replacement at the remembered key preserves
	// the user command and installs the new nexo key.
	if output, err := runTmux("bind-key", "-n", "C-w", "display-message", "user changed binding"); err != nil {
		t.Fatalf("install changed user binding: %v\n%s", err, output)
	}
	writeConfig("C-v", "M-a")
	oldUserPlace, _ = runTmux("list-keys", "-T", "root", "C-w")
	newPlace, _ = runTmux("list-keys", "-T", "root", "C-v")
	if !strings.Contains(string(oldUserPlace), "user changed binding") || !strings.Contains(string(newPlace), "NEXO_PLACE_PICKER_INLINE=1") {
		t.Fatalf("changed-key user bindings: old=%q new=%q", oldUserPlace, newPlace)
	}

	// A changed key with no user replacement removes only the old nexo key.
	writeConfig("C-y", "M-a")
	oldNexoPlace, _ := runTmux("list-keys", "-T", "root", "C-v")
	newPlace, _ = runTmux("list-keys", "-T", "root", "C-y")
	if !strings.Contains(string(oldNexoPlace), "unknown key") || !strings.Contains(string(newPlace), "NEXO_PLACE_PICKER_INLINE=1") {
		t.Fatalf("changed-key nexo bindings: old=%q new=%q", oldNexoPlace, newPlace)
	}

	// Disabling an unmodified nexo key removes it.
	writeConfig("none", "M-a")
	removedPlace, _ := runTmux("list-keys", "-T", "root", "C-y")
	if !strings.Contains(string(removedPlace), "unknown key") {
		t.Fatalf("disabled nexo place binding = %q, want removed", removedPlace)
	}

	// Disabling a key after a user replacement preserves the user command.
	writeConfig("C-u", "M-a")
	if output, err := runTmux("bind-key", "-n", "C-u", "display-message", "user none binding"); err != nil {
		t.Fatalf("install user none binding: %v\n%s", err, output)
	}
	writeConfig("none", "M-a")
	userNonePlace, _ := runTmux("list-keys", "-T", "root", "C-u")
	if !strings.Contains(string(userNonePlace), "user none binding") {
		t.Fatalf("disabled user binding = %q, want preserved binding", userNonePlace)
	}

	// The same ownership rules apply to the agent navigator binding.
	if output, err := runTmux("bind-key", "M-a", "display-message", "user agent binding"); err != nil {
		t.Fatalf("install user agent binding: %v\n%s", err, output)
	}
	writeConfig("none", "none")
	userNoneAgent, _ := runTmux("list-keys", "-T", "prefix", "M-a")
	if !strings.Contains(string(userNoneAgent), "user agent binding") {
		t.Fatalf("disabled user agent binding = %q, want preserved binding", userNoneAgent)
	}
}

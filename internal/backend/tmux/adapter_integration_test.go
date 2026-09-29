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

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
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

	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
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
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))

	configPath := filepath.Join(t.TempDir(), "config.toml")
	agentsConfig := "[agents]\nprocesses = [\"pi\"]\nidle-seconds = 17\npoll-interval = 1\nnotify = false\n\n"
	if err := os.WriteFile(configPath, []byte(agentsConfig+"[tmux]\nplace-key = \"C-s\"\nagent-key = \"a\"\npopup-width = \"70%\"\npopup-height = \"60%\"\npopup-x = \"C\"\npopup-y = \"C\"\n"), 0o600); err != nil {
		t.Fatal(err)
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
	// Existing indexed hooks belong to the user and must survive installation.
	for _, args := range [][]string{
		{"set-hook", "-g", "pane-focus-in[0]", "run-shell 'user focus zero'"},
		{"set-hook", "-g", "pane-focus-in[4]", "run-shell 'user focus four'"},
		{"set-hook", "-g", "client-session-changed[0]", "run-shell 'user session zero'"},
		{"set-hook", "-g", "client-session-changed[3]", "run-shell 'user session three'"},
	} {
		if output, err := runTmux(args...); err != nil {
			t.Fatalf("install existing hook: %v\n%s", err, output)
		}
	}
	// These user commands contain misleading nexo text but are not adapter
	// popup bindings, so first-run initialization must preserve them.
	if output, err := runTmux("bind-key", "-n", "C-s", "run-shell", "nexo --backend=tmux agents --json"); err != nil {
		t.Fatalf("install command-like user binding: %v\n%s", err, output)
	}
	if output, err := runTmux("bind-key", "a", "display-message", "NEXO_AGENT_NAVIGATOR_INLINE=1 is a user message"); err != nil {
		t.Fatalf("install marker-like user binding: %v\n%s", err, output)
	}
	command := fmt.Sprintf("%s --backend=tmux --config-file=%s tmux init", binary, configPath)
	if output, err := runTmux("run-shell", command); err != nil {
		t.Fatalf("run adapter: %v\n%s", err, output)
	}
	time.Sleep(300 * time.Millisecond)
	falsePlace, _ := runTmux("list-keys", "-T", "root", "C-s")
	falseAgent, _ := runTmux("list-keys", "-T", "prefix", "a")
	if !strings.Contains(string(falsePlace), "agents --json") || strings.Contains(string(falsePlace), "display-popup") {
		t.Fatalf("false-positive place binding = %q, want user command preserved", falsePlace)
	}
	if !strings.Contains(string(falseAgent), "is a user message") || strings.Contains(string(falseAgent), "display-popup") {
		t.Fatalf("false-positive agent binding = %q, want user message preserved", falseAgent)
	}

	writeInitialConfig := func(placeKey, agentKey string) {
		t.Helper()
		contents := agentsConfig + fmt.Sprintf("[tmux]\nplace-key = %q\nagent-key = %q\npopup-width = \"70%%\"\npopup-height = \"60%%\"\npopup-x = \"C\"\npopup-y = \"C\"\n", placeKey, agentKey)
		if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := runTmux("run-shell", command); err != nil {
			t.Fatalf("re-run adapter: %v\n%s", err, output)
		}
		time.Sleep(300 * time.Millisecond)
	}
	writeInitialConfig("C-x", "b")

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
		text := string(output)
		if !strings.Contains(text, "NEXO_AGENT_FOCUS_HOOK=1") || !strings.Contains(text, "--backend='tmux'") || !strings.Contains(text, "--config-file='") {
			t.Fatalf("%s = %q, want configured nexo focus hook", name, output)
		}
	}
	for _, want := range []string{"user focus zero", "user focus four"} {
		if !strings.Contains(string(focusHook), want) {
			t.Fatalf("pane hooks = %q, want preserved %q", focusHook, want)
		}
	}
	for _, want := range []string{"user session zero", "user session three"} {
		if !strings.Contains(string(changedHook), want) {
			t.Fatalf("session hooks = %q, want preserved %q", changedHook, want)
		}
	}
	lockFiles, err := filepath.Glob(filepath.Join(stateHome, "nexo", "agents", "*.lock"))
	if err != nil || len(lockFiles) == 0 {
		t.Fatalf("watcher lock files = %v, err = %v, want running watcher", lockFiles, err)
	}

	// A second install moves the remembered bindings before the user replaces
	// the new place key. This makes the next run a real same-key replacement.
	writeInitialConfig("C-z", "M-a")
	oldPlace, _ := runTmux("list-keys", "-T", "root", "C-x")
	oldAgent, _ := runTmux("list-keys", "-T", "prefix", "b")
	rememberedPlace, _ := runTmux("list-keys", "-T", "root", "C-z")
	newAgent, _ := runTmux("list-keys", "-T", "prefix", "M-a")
	if !strings.Contains(string(oldPlace), "unknown key") || !strings.Contains(string(oldAgent), "unknown key") {
		t.Fatalf("old nexo bindings remain: place=%q agent=%q", oldPlace, oldAgent)
	}
	if !strings.Contains(string(rememberedPlace), "NEXO_PLACE_PICKER_INLINE=1") || !strings.Contains(string(newAgent), "NEXO_AGENT_NAVIGATOR_INLINE=1") {
		t.Fatalf("remembered bindings: place=%q agent=%q", rememberedPlace, newAgent)
	}
	if output, err := runTmux("bind-key", "-n", "C-z", "display-message", "user binding"); err != nil {
		t.Fatalf("install user binding: %v\n%s", err, output)
	}

	writeConfig := func(placeKey, agentKey string) {
		t.Helper()
		contents := agentsConfig + fmt.Sprintf("[tmux]\nplace-key = %q\nagent-key = %q\npopup-width = \"70%%\"\npopup-height = \"60%%\"\npopup-x = \"C\"\npopup-y = \"C\"\n", placeKey, agentKey)
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
	userPlace, _ := runTmux("list-keys", "-T", "root", "C-z")
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
	lockFiles, err = filepath.Glob(filepath.Join(stateHome, "nexo", "agents", "*.lock"))
	watcherLocks := 0
	for _, path := range lockFiles {
		if !strings.HasSuffix(path, ".state.lock") {
			watcherLocks++
		}
	}
	if err != nil || watcherLocks != 1 {
		t.Fatalf("watcher lock files after repeated init = %v, err = %v, want one watcher", lockFiles, err)
	}
}

func TestTmuxAdapterSpecialKeys(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		_ = os.Unsetenv(name)
	}
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("PATH", os.Getenv("PATH"))
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[tmux]\nplace-key = \";\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := fmt.Sprintf("nexo-special-%d", time.Now().UnixNano())
	runTmux := func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", commandArgs...).CombinedOutput()
	}
	if output, err := runTmux("new-session", "-d", "-s", "work", "sleep", "30"); err != nil {
		t.Fatalf("create throwaway tmux server: %v\n%s", err, output)
	}
	defer func() { _, _ = runTmux("kill-server") }()
	r := runner.Func(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = append([]string{"-L", server, "-f", "/dev/null"}, args...)
		}
		return runner.ExecRunner{}.Run(ctx, name, args...)
	})
	backend := New(r, true)
	for _, key := range []string{";", "'", "\\", ":", "-"} {
		if err := backend.bind(ctx, "-n", "root", placeBindingOption, key, "tmux.place-key", pickerCommand(configPath), config.TmuxConfig{}); err != nil {
			t.Fatalf("bind key %q: %v", key, err)
		}
		output, err := runTmux("list-keys", "-T", "root", tmuxKey(key))
		if err != nil || !strings.Contains(string(output), "display-popup") {
			t.Fatalf("key %q binding = %q, err = %v", key, output, err)
		}
	}
}

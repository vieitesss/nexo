package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunOpenNoFocusJSONCreatesDetachedSession exercises `nexo open --no-focus
// --json` against a throwaway tmux server: the first call creates a detached
// session and the second reuses it.
func TestRunOpenNoFocusJSONCreatesDetachedSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	server := fmt.Sprintf("nexo-open-nofocus-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", server, "kill-server").Run()
	})
	if output, err := exec.Command("tmux", "-L", server, "-f", "/dev/null", "new-session", "-d", "-s", "warmup", "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("start throwaway tmux server: %v\n%s", err, output)
	}
	tmuxRunner := funcRunner(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		full := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", full...).CombinedOutput()
	})

	root := t.TempDir()
	candidate := filepath.Join(root, "app")
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		t.Fatal(err)
	}
	candidate = resolved
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("paths = [%q]\n", filepath.Dir(candidate))), 0o600); err != nil {
		t.Fatal(err)
	}

	type opened struct {
		Backend string `json:"backend"`
		ID      string `json:"id"`
		Name    string `json:"name"`
		Path    string `json:"path"`
		Created bool   `json:"created"`
	}
	open := func() opened {
		t.Helper()
		var stdout, stderr strings.Builder
		status := RunWith(
			[]string{"--backend=tmux", "-c", configPath, "--json", "open", candidate, "--no-focus"},
			&stdout, &stderr, tmuxRunner, mapEnvironment{}, "",
		)
		if status != 0 {
			t.Fatalf("status = %d, stdout = %q, stderr = %q", status, stdout.String(), stderr.String())
		}
		var result opened
		if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
			t.Fatalf("decode %q: %v", stdout.String(), err)
		}
		return result
	}

	first := open()
	if first.Backend != "tmux" || first.ID == "" || first.Name == "" || first.Path != candidate || !first.Created {
		t.Fatalf("first open = %#v, want created detached tmux session", first)
	}
	listing, err := exec.Command("tmux", "-L", server, "list-sessions", "-F", "#{session_id}\t#{session_name}").Output()
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
		if strings.HasPrefix(line, first.ID+"\t") {
			found = true
		}
	}
	if !found {
		t.Fatalf("sessions = %q, want %s detached", listing, first.ID)
	}

	second := open()
	if second.ID != first.ID || second.Created {
		t.Fatalf("second open = %#v, want reused %s with created=false", second, first.ID)
	}
}

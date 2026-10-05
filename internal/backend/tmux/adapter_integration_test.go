package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestTmuxAdapterInitIsIdempotentAndPreservesIndexedHooks(t *testing.T) {
	// A legacy hook is one an older nexo installed without -b and with a
	// command the current binary no longer accepts.
	const legacyHook = `run-shell "NEXO_AGENT_FOCUS_HOOK=1 'nexo' --backend='tmux' agents seen #{pane_id}"`
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	server := testSocket(t, "adapter")
	args := func(command ...string) []string { return append([]string{"-L", server, "-f", "/dev/null"}, command...) }
	run := func(command ...string) ([]byte, error) {
		return exec.Command("tmux", args(command...)...).CombinedOutput()
	}
	if output, err := run("new-session", "-d", "-s", "work", "sleep", "30"); err != nil {
		t.Fatalf("create throwaway tmux server: %v\n%s", err, output)
	}
	defer func() { _, _ = run("kill-server") }()
	for _, command := range [][]string{{"set-option", "-g", "focus-events", "on"}, {"set-hook", "-g", "pane-focus-in[3]", "display-message user-focus"}, {"set-hook", "-g", "client-session-changed[4]", "display-message user-session"}, {"set-hook", "-g", "pane-focus-in[0]", legacyHook}, {"set-hook", "-g", "client-session-changed[0]", legacyHook}} {
		if output, err := run(command...); err != nil {
			t.Fatalf("%v: %v\n%s", command, err, output)
		}
	}
	tmuxRunner := funcRunner(func(ctx context.Context, _ string, command ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "tmux", args(command...)...).CombinedOutput()
	})
	backend := New(tmuxRunner, true)
	for i := 0; i < 2; i++ {
		if err := backend.Init(context.Background(), capabilities.InitInput{ConfigPath: ""}); err != nil {
			t.Fatalf("Init() #%d error = %v", i+1, err)
		}
	}
	for _, hook := range []string{"pane-focus-in", "client-session-changed"} {
		output := string(mustRun(t, run, "show-hooks", "-g", hook))
		if strings.Count(output, "NEXO_AGENT_FOCUS_HOOK=1") != 1 {
			t.Fatalf("%s hooks = %q, want one nexo hook after repeated init", hook, output)
		}
		if strings.Contains(output, "agents seen") {
			t.Fatalf("%s hooks = %q, want the legacy nexo hook replaced", hook, output)
		}
	}
	focus := string(mustRun(t, run, "show-hooks", "-g", "pane-focus-in"))
	if !strings.Contains(focus, "pane-focus-in[3] display-message user-focus") {
		t.Fatalf("pane-focus-in hooks = %q, want indexed user hook preserved", focus)
	}
	session := string(mustRun(t, run, "show-hooks", "-g", "client-session-changed"))
	if !strings.Contains(session, "client-session-changed[4] display-message user-session") {
		t.Fatalf("client-session-changed hooks = %q, want indexed user hook preserved", session)
	}
}

func mustRun(t *testing.T, run func(...string) ([]byte, error), command ...string) []byte {
	t.Helper()
	output, err := run(command...)
	if err != nil {
		t.Fatalf("%v failed: %v\n%s", command, err, output)
	}
	return output
}

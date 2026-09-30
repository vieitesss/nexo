package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

func TestTmuxAdapterInitMigratesOwnedBindingOnThrowawayServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	server := fmt.Sprintf("nexo-test-%d", time.Now().UnixNano())
	args := func(command ...string) []string {
		return append([]string{"-L", server, "-f", "/dev/null"}, command...)
	}
	run := func(command ...string) ([]byte, error) {
		return exec.Command("tmux", args(command...)...).CombinedOutput()
	}
	if output, err := run("new-session", "-d", "-s", "work", "sleep", "30"); err != nil {
		t.Fatalf("create throwaway tmux server: %v\n%s", err, output)
	}
	defer func() { _, _ = run("kill-server") }()

	if output, err := run("bind-key", "-T", "root", "C-s", "display-message", "old"); err != nil {
		t.Fatalf("install binding: %v\n%s", err, output)
	}
	line, err := run("list-keys", "-T", "root", "C-s")
	if err != nil {
		t.Fatalf("read binding: %v\n%s", err, line)
	}
	if output, err := run("set-option", "-g", placeBindingOption, encodeBindingRecord("root", "C-s", string(line))); err != nil {
		t.Fatalf("record binding: %v\n%s", err, output)
	}

	tmuxRunner := runner.Func(func(ctx context.Context, _ string, command ...string) ([]byte, error) {
		request := exec.CommandContext(ctx, "tmux", args(command...)...)
		return request.CombinedOutput()
	})
	if err := New(tmuxRunner, true).Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	remaining, err := run("list-keys", "-T", "root", "C-s")
	if err == nil || !strings.Contains(string(remaining), "unknown key") {
		t.Fatalf("owned binding = %q, want removed", remaining)
	}
	record := mustRun(t, run, "show-option", "-gqv", placeBindingOption)
	if strings.TrimSpace(string(record)) != "" {
		t.Fatalf("ownership record = %q, want deleted", record)
	}
}

func TestTmuxAdapterInitKeepsChangedBindingOnThrowawayServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	server := fmt.Sprintf("nexo-test-%d", time.Now().UnixNano())
	args := func(command ...string) []string {
		return append([]string{"-L", server, "-f", "/dev/null"}, command...)
	}
	run := func(command ...string) ([]byte, error) {
		return exec.Command("tmux", args(command...)...).CombinedOutput()
	}
	if output, err := run("new-session", "-d", "-s", "work", "sleep", "30"); err != nil {
		t.Fatalf("create throwaway tmux server: %v\n%s", err, output)
	}
	defer func() { _, _ = run("kill-server") }()
	if output, err := run("bind-key", "-T", "root", "C-s", "display-message", "user"); err != nil {
		t.Fatalf("install binding: %v\n%s", err, output)
	}
	owned := "bind-key -T root C-s display-message old\n"
	if output, err := run("set-option", "-g", placeBindingOption, encodeBindingRecord("root", "C-s", owned)); err != nil {
		t.Fatalf("record binding: %v\n%s", err, output)
	}
	tmuxRunner := runner.Func(func(ctx context.Context, _ string, command ...string) ([]byte, error) {
		request := exec.CommandContext(ctx, "tmux", args(command...)...)
		return request.CombinedOutput()
	})
	if err := New(tmuxRunner, true).Init(context.Background(), InitInput{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	line := mustRun(t, run, "list-keys", "-T", "root", "C-s")
	if !strings.Contains(string(line), "user") {
		t.Fatalf("changed binding = %q, want user binding", line)
	}
	if record := mustRun(t, run, "show-option", "-gqv", placeBindingOption); strings.TrimSpace(string(record)) != "" {
		t.Fatalf("ownership record = %q, want deleted", record)
	}
}

func mustRun(t *testing.T, run func(...string) ([]byte, error), command ...string) []byte {
	t.Helper()
	output, _ := run(command...)
	return output
}

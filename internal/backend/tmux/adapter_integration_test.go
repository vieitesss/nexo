package tmux

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	options := mustRun(t, run, "show-options", "-g")
	if strings.Contains(string(options), placeBindingOption) || strings.Contains(string(options), agentBindingOption) {
		t.Fatalf("ownership options = %q, want absent", options)
	}
}

func TestTmuxAdapterInitIsIdempotentAndPreservesIndexedHooks(t *testing.T) {
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
	for _, command := range [][]string{
		{"set-option", "-g", "focus-events", "on"},
		{"set-hook", "-g", "pane-focus-in[3]", "display-message user-focus"},
		{"set-hook", "-g", "client-session-changed[4]", "display-message user-session"},
	} {
		if output, err := run(command...); err != nil {
			t.Fatalf("%v: %v\n%s", command, err, output)
		}
	}
	tmuxRunner := runner.Func(func(ctx context.Context, _ string, command ...string) ([]byte, error) {
		request := exec.CommandContext(ctx, "tmux", args(command...)...)
		return request.CombinedOutput()
	})
	backend := New(tmuxRunner, true)
	for i := 0; i < 2; i++ {
		if err := backend.Init(context.Background(), InitInput{Stderr: io.Discard}); err != nil {
			t.Fatalf("Init() #%d error = %v", i+1, err)
		}
	}
	for _, hook := range []string{"pane-focus-in", "client-session-changed"} {
		output := string(mustRun(t, run, "show-hooks", "-g", hook))
		if strings.Count(output, "NEXO_AGENT_FOCUS_HOOK=1") != 1 {
			t.Fatalf("%s hooks = %q, want one nexo hook after repeated init", hook, output)
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

func TestTmuxAdapterInitWarnsForLegacyRecordOnThrowawayServer(t *testing.T) {
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
	if output, err := run("bind-key", "-T", "prefix", "a", "display-message", "legacy"); err != nil {
		t.Fatalf("install legacy binding: %v\n%s", err, output)
	}
	if output, err := run("set-option", "-g", agentBindingOption, "prefix:a"); err != nil {
		t.Fatalf("record legacy binding: %v\n%s", err, output)
	}
	tmuxRunner := runner.Func(func(ctx context.Context, _ string, command ...string) ([]byte, error) {
		request := exec.CommandContext(ctx, "tmux", args(command...)...)
		return request.CombinedOutput()
	})
	var stderr bytes.Buffer
	if err := New(tmuxRunner, true).Init(context.Background(), InitInput{Stderr: &stderr}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if got := strings.Count(stderr.String(), "nexo no longer binds keys"); got != 1 {
		t.Fatalf("warning count = %d, stderr = %q; want one", got, stderr.String())
	}
	remaining := mustRun(t, run, "list-keys", "-T", "prefix", "a")
	if !strings.Contains(string(remaining), "legacy") {
		t.Fatalf("legacy binding = %q, want untouched", remaining)
	}
	options := mustRun(t, run, "show-options", "-g")
	if strings.Contains(string(options), agentBindingOption) {
		t.Fatalf("ownership options = %q, want absent", options)
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
	if output, err := run("bind-key", "-T", "root", "C-s", "display-message", "old"); err != nil {
		t.Fatalf("install binding: %v\n%s", err, output)
	}
	owned, err := run("list-keys", "-T", "root", "C-s")
	if err != nil {
		t.Fatalf("read binding: %v\n%s", err, owned)
	}
	if output, err := run("set-option", "-g", placeBindingOption, encodeBindingRecord("root", "C-s", string(owned))); err != nil {
		t.Fatalf("record binding: %v\n%s", err, output)
	}
	if output, err := run("bind-key", "-T", "root", "C-s", "display-message", "user"); err != nil {
		t.Fatalf("modify binding: %v\n%s", err, output)
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
	options := mustRun(t, run, "show-options", "-g")
	if strings.Contains(string(options), placeBindingOption) || strings.Contains(string(options), agentBindingOption) {
		t.Fatalf("ownership options = %q, want absent", options)
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

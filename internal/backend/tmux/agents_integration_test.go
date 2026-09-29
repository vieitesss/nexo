package tmux

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

func TestTmuxAgentIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	ctx := context.Background()
	server := "nexo-test-" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	pi := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(pi, []byte("#!/bin/sh\nprintf '⠋ working\\n'\nsleep 1\nprintf '\\033[2J\\033[Hprompt> \\n'\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "nexo")
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/nexo")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build nexo: %v\n%s", err, output)
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))

	runTmux := func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", commandArgs...).CombinedOutput()
	}
	if _, err := runTmux("new-session", "-d", "-s", "work", pi); err != nil {
		t.Fatal(err)
	}
	control := exec.Command("tmux", "-C", "-L", server, "-f", "/dev/null", "attach-session", "-t", "work")
	controlInput, err := control.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	control.Stdout = io.Discard
	control.Stderr = io.Discard
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	defer func() {
		_ = controlInput.Close()
		_ = control.Process.Kill()
		_, _ = runTmux("kill-server")
	}()

	source := NewAgentSource(runner.Func(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = append([]string{"-L", server, "-f", "/dev/null"}, args...)
		}
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}))
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})

	agents, err := source.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Harness != "pi" {
		t.Fatalf("discovery = %#v, want one pi agent", agents)
	}
	if err := source.installHooks(ctx); err != nil {
		t.Fatal(err)
	}
	focus, err := runTmux("show-option", "-gqv", "focus-events")
	if err != nil || strings.TrimSpace(string(focus)) != "on" {
		t.Fatalf("focus-events = %q, err = %v, want on", focus, err)
	}

	time.Sleep(2 * time.Second)
	agents, err = source.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].State != "stopped" {
		t.Fatalf("stopped discovery = %#v, want one stopped agent", agents)
	}

	if output, err := runTmux("new-session", "-d", "-s", "other", "sleep 30"); err != nil {
		t.Fatalf("create focus session: %v: %s", err, output)
	}
	clients, err := runTmux("list-clients", "-F", "#{client_name}")
	if err != nil || strings.TrimSpace(string(clients)) == "" {
		t.Fatalf("list control clients: %v: %s", err, clients)
	}
	client := strings.TrimSpace(strings.Split(string(clients), "\n")[0])
	if output, err := runTmux("switch-client", "-c", client, "-t", "other"); err != nil {
		t.Fatalf("switch to other: %v: %s", err, output)
	}
	if output, err := runTmux("switch-client", "-c", client, "-t", "work"); err != nil {
		t.Fatalf("switch to work: %v: %s", err, output)
	}
	time.Sleep(500 * time.Millisecond)
	agents, err = source.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || !agents[0].Seen {
		t.Fatalf("focus result = %#v, want seen agent", agents)
	}
}

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

// fakeHarness creates an executable named pi whose argv[0] basename is the
// harness name, the way a real agent starts. A symlink avoids a shell script,
// whose argv[0] would be the interpreter rather than the harness.
func fakeHarness(t *testing.T) string {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is not installed")
	}
	pi := filepath.Join(t.TempDir(), "pi")
	if err := os.Symlink(sleep, pi); err != nil {
		t.Fatal(err)
	}
	return pi
}

func TestTmuxAgentPathWithTab(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-tab-path-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", commandArgs...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	path := filepath.Join(t.TempDir(), "a\tb")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	pi := fakeHarness(t)
	if output, err := runTmux("new-session", "-d", "-s", "work", "-c", path, pi+" 600"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}

	source := NewAgentSource(runner.Func(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = append([]string{"-L", server, "-f", "/dev/null"}, args...)
		}
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}))
	source.SetStateDir(t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})
	agents, err := source.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Container != "work" || agents[0].Target != "work:0.0" {
		t.Fatalf("discovery = %#v, want one agent in the tabbed-path pane", agents)
	}
}

func TestTmuxAgentIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	ctx := context.Background()
	server := "nexo-test-" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := os.MkdirAll(filepath.Join(configHome, "nexo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "nexo", "config.toml"), []byte("[agents]\nprocesses = [\"pi\"]\nidle-seconds = 1\npoll-interval = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pi := fakeHarness(t)
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
	if _, err := runTmux("new-session", "-d", "-s", "work", pi+" 600"); err != nil {
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
	source.ConfigureHookCommand(binary, "")

	agents, err := source.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Harness != "pi" {
		t.Fatalf("discovery = %#v, want one pi agent", agents)
	}
	if output, err := runTmux("set-option", "-g", "focus-events", "on"); err != nil {
		t.Fatalf("enable focus events for focus-hook test: %v: %s", err, output)
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

func TestTmuxAgentFindsHarnessInDescendantProcess(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-descendant-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", commandArgs...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	if output, err := runTmux("new-session", "-d", "-s", "work", "bash"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}
	// The interactive shell stays the pane's main process while the harness
	// runs as a descendant, the way a user starts an agent from a prompt.
	if output, err := runTmux("send-keys", "-t", "work:0.0", "bash -c 'exec -a pi sleep 30' & wait", "Enter"); err != nil {
		t.Fatalf("start descendant: %v: %s", err, output)
	}

	source := NewAgentSource(runner.Func(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = append([]string{"-L", server, "-f", "/dev/null"}, args...)
		}
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}))
	source.SetStateDir(t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})
	deadline := time.Now().Add(5 * time.Second)
	for {
		agents, err := source.ListAgents(ctx)
		if err != nil {
			t.Fatalf("ListAgents() error = %v", err)
		}
		if len(agents) == 1 && agents[0].Harness == "pi" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery = %#v, want descendant pi process", agents)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

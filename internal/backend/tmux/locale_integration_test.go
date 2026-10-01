package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

// localeLessRunner runs tmux on a throwaway server with LANG, LC_ALL and
// LC_CTYPE removed from the child environment. This reproduces a
// non-interactive SSH shell, where tmux otherwise replaces the tab separators
// in -F output with underscores and every parsed line is lost.
type localeLessRunner struct {
	server string
}

func (r localeLessRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		args = append([]string{"-L", r.server, "-f", "/dev/null"}, args...)
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Env = localeLessEnv()
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Stderr, err
		}
		return nil, err
	}
	return output, nil
}

func localeLessEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, entry := range env {
		switch {
		case strings.HasPrefix(entry, "LANG="),
			strings.HasPrefix(entry, "LC_ALL="),
			strings.HasPrefix(entry, "LC_CTYPE="):
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// TestListingSurvivesLocaleLessTmux proves that agent, container and proxy
// discovery keep working when tmux runs without a UTF-8 locale. Removing the
// global -u flag makes every tab separator an underscore and this test fails.
func TestListingSurvivesLocaleLessTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	ctx := context.Background()
	server := "nexo-test-locale-" + strings.ReplaceAll(t.Name(), "/", "-")
	commandRunner := localeLessRunner{server: server}
	t.Cleanup(func() {
		_, _ = commandRunner.Run(ctx, "tmux", "kill-server")
	})

	pi := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(pi, []byte("#!/bin/sh\nprintf '⠋ working\\n'\nsleep 600\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := commandRunner.Run(ctx, "tmux", "new-session", "-d", "-s", "work", pi); err != nil {
		t.Fatalf("start agent session: %v", err)
	}
	if _, err := commandRunner.Run(ctx, "tmux", "new-session", "-d", "-s", "plain", "sleep 600"); err != nil {
		t.Fatalf("start plain session: %v", err)
	}
	if _, err := commandRunner.Run(ctx, "tmux", "new-session", "-d", "-s", "devbox/prod", "sleep 600"); err != nil {
		t.Fatalf("start proxy session: %v", err)
	}
	if _, err := commandRunner.Run(ctx, "tmux", "set-option", "-t", "devbox/prod", proxySessionOption, "devbox/prod"); err != nil {
		t.Fatalf("tag proxy session: %v", err)
	}

	source := NewAgentSource(commandRunner)
	source.SetStateDir(t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 3, PollInterval: 1})
	deadline := time.Now().Add(2 * time.Second)
	var agents []capabilities.Agent
	for time.Now().Before(deadline) {
		var err error
		agents, err = source.ListAgents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(agents) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(agents) != 1 || agents[0].Harness != "pi" {
		t.Fatalf("agents = %#v, want one pi agent", agents)
	}

	backend := New(commandRunner, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"devbox"}}, "")
	containers, err := backend.ListContainers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundPlain, foundProxy := false, false
	for _, container := range containers {
		if !container.Proxy && container.Name == "plain" {
			foundPlain = true
		}
		if container.Proxy && container.Name == "prod" && container.Machine == "devbox" {
			foundProxy = true
		}
	}
	if len(containers) != 3 || !foundPlain || !foundProxy {
		t.Fatalf("containers = %#v, want plain container and devbox/prod proxy", containers)
	}
}

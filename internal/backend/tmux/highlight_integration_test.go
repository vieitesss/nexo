package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

// TestTmuxUnseenWindowHighlight drives a throwaway tmux server to pin the
// pieces a fake runner cannot: tmux's expansion of the highlight format
// variables, several panes sharing one window, and clearing only when the
// exact unseen pane is focused.
func TestTmuxUnseenWindowHighlight(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-highlight-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.CommandContext(ctx, "tmux", commandArgs...).CombinedOutput()
	}
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	pi := fakeHarness(t)
	if output, err := runTmux("new-session", "-d", "-s", "work", pi+" 600"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}
	// Two agents share one window so focusing either pane must be checked
	// against the other, and a second window proves clearing stays local.
	if output, err := runTmux("split-window", "-t", "work:0", pi+" 600"); err != nil {
		t.Fatalf("split window: %v: %s", err, output)
	}
	if output, err := runTmux("new-window", "-t", "work", "-n", "other", pi+" 600"); err != nil {
		t.Fatalf("create second window: %v: %s", err, output)
	}

	source := NewAgentSource(funcRunner(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = append([]string{"-L", server, "-f", "/dev/null"}, args...)
		}
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}))
	setStateDir(source, t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})

	deadline := time.Now().Add(15 * time.Second)
	for {
		agents, err := source.listAgents(ctx, true)
		if err != nil {
			t.Fatalf("listAgents() error = %v", err)
		}
		if stoppedUnseen(agents) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agents = %#v, want three unseen stopped agents", agents)
		}
		time.Sleep(200 * time.Millisecond)
	}

	build := windowByPane(t, runTmux, "work:0.0")
	other := windowByPane(t, runTmux, "work:1.0")
	windows := readWindowHighlights(t, runTmux)
	if windows[build].marker != "1" || windows[build].style != "fg=red" {
		t.Fatalf("build window highlight = %#v, want marker and fg=red", windows[build])
	}
	if windows[other].marker != "1" {
		t.Fatalf("other window highlight = %#v, want highlighted", windows[other])
	}

	// Marking one pane of the shared window seen must not clear the window
	// while its sibling is still unseen: clearing is per exact pane.
	paneA, paneB := windowPanes(t, runTmux, build)
	if err := source.MarkSeen(ctx, paneA); err != nil {
		t.Fatalf("MarkSeen(%s) error = %v", paneA, err)
	}
	if _, err := source.listAgents(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := readWindowHighlights(t, runTmux)[build]; got.marker != "1" {
		t.Fatalf("highlight after marking %s = %#v, want still highlighted while %s is unseen", paneA, got, paneB)
	}

	if err := source.MarkSeen(ctx, paneB); err != nil {
		t.Fatalf("MarkSeen(%s) error = %v", paneB, err)
	}
	if _, err := source.listAgents(ctx, true); err != nil {
		t.Fatal(err)
	}
	cleared := readWindowHighlights(t, runTmux)
	if got := cleared[build]; got.marker != "" || got.style != "default" {
		t.Fatalf("highlight after marking both panes = %#v, want cleared", got)
	}
	if got := cleared[other]; got.marker != "1" {
		t.Fatalf("other window = %#v, want focus in another window to leave it highlighted", got)
	}
}

type windowHighlightState struct {
	marker string
	style  string
}

func stoppedUnseen(agents []capabilities.Agent) int {
	count := 0
	for _, agent := range agents {
		if agent.State == "stopped" && !agent.Seen {
			count++
		}
	}
	return count
}

// readWindowHighlights asserts tmux expands the highlight format variables by
// reading the marker and effective style back with the same format nexo uses.
func readWindowHighlights(t *testing.T, runTmux func(...string) ([]byte, error)) map[string]windowHighlightState {
	t.Helper()
	output, err := runTmux("list-windows", "-a", "-F", windowHighlightFormat)
	if err != nil {
		t.Fatalf("list windows: %v: %s", err, output)
	}
	result := make(map[string]windowHighlightState)
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		result[fields[0]] = windowHighlightState{marker: strings.TrimSpace(fields[1]), style: strings.TrimSpace(fields[2])}
	}
	return result
}

func windowByPane(t *testing.T, runTmux func(...string) ([]byte, error), target string) string {
	t.Helper()
	output, err := runTmux("list-panes", "-t", target, "-F", "#{window_id}")
	if err != nil {
		t.Fatalf("list pane %s: %v: %s", target, err, output)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		t.Fatalf("list pane %s: no window id in %q", target, output)
	}
	return fields[0]
}

func windowPanes(t *testing.T, runTmux func(...string) ([]byte, error), windowID string) (string, string) {
	t.Helper()
	output, err := runTmux("list-panes", "-t", windowID, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list panes for %s: %v: %s", windowID, err, output)
	}
	panes := strings.Fields(string(output))
	if len(panes) != 2 {
		t.Fatalf("panes in %s = %v, want two", windowID, panes)
	}
	return panes[0], panes[1]
}

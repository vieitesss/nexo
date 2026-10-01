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
	runTmux := throwawayTmux(server)
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

	source := NewAgentSource(throwawayRunner(server))
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

// TestTmuxUnseenWindowHighlightFocusHook drives real pane focus through the
// installed hook and the watcher on a throwaway server. It pins that one
// sibling stays unseen, that another window stays marked, and that clearing
// unsets both local style options so custom inherited styles return.
func TestTmuxUnseenWindowHighlightFocusHook(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-hl-focus-" + strings.ReplaceAll(t.Name(), "/", "-")
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)
	binary := buildNexoBinary(t)
	runTmux := throwawayTmux(server)
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	pi := fakeHarness(t)
	workA := strings.TrimSpace(string(mustRun(t, runTmux, "new-session", "-d", "-s", "work", "-P", "-F", "#{pane_id}", pi+" 600")))
	workB := strings.TrimSpace(string(mustRun(t, runTmux, "split-window", "-t", "work:0", "-P", "-F", "#{pane_id}", pi+" 600")))
	// A non-agent third pane is left active so switching the client into the
	// window focuses it before the targeted agent pane.
	mustRun(t, runTmux, "split-window", "-t", "work:0", "-P", "-F", "#{pane_id}", "sleep 600")
	mustRun(t, runTmux, "new-window", "-t", "work", "-n", "other", pi+" 600")
	// Custom inherited styles so clearing can be asserted to restore them.
	mustRun(t, runTmux, "set-option", "-g", "window-status-style", "bg=colour17")
	mustRun(t, runTmux, "set-option", "-g", "window-status-current-style", "bg=colour18")
	mustRun(t, runTmux, "set-option", "-g", "focus-events", "on")

	source := NewAgentSource(throwawayRunner(server))
	setStateDir(source, filepath.Join(stateRoot, "nexo", "agents"))
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})
	// tmux's run-shell may not set TMUX, so the hook wrapper pins the throwaway
	// socket before running the real binary. This keeps the hook's ledger and
	// server identity equal to the watcher's without touching any other server.
	socket, err := runTmux("display-message", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("read socket path: %v: %s", err, socket)
	}
	hookWrapper := filepath.Join(t.TempDir(), "hook.sh")
	wrapper := "#!/bin/sh\nTMUX='" + strings.TrimSpace(string(socket)) + "' exec '" + binary + "' \"$@\"\n"
	if err := os.WriteFile(hookWrapper, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	source.ConfigureHookCommand(hookWrapper, "")

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
	defer func() {
		_ = controlInput.Close()
		_ = control.Process.Kill()
	}()
	time.Sleep(200 * time.Millisecond)

	if err := source.installHooks(ctx); err != nil {
		t.Fatalf("installHooks() error = %v", err)
	}
	clients, err := runTmux("list-clients", "-F", "#{client_name}")
	if err != nil || strings.TrimSpace(string(clients)) == "" {
		t.Fatalf("list control client: %v: %s", err, clients)
	}

	waitForUnseenAgents(t, source, ctx, 3)
	build := windowByPane(t, runTmux, workA)
	other := windowByPane(t, runTmux, "work:1.0")

	// A real pane focus drives the installed hook. Selecting the window first
	// focuses its active pane, which is the non-agent pane, so the following
	// select-pane is the only agent focus event.
	focus := func(pane string) {
		t.Helper()
		mustRun(t, runTmux, "select-window", "-t", "work:0")
		mustRun(t, runTmux, "select-pane", "-t", pane)
	}

	// Focus one sibling. The hook marks only that pane, so the window must stay
	// highlighted while workB is unseen.
	focus(workA)
	waitForSeenPane(t, source, ctx, workA)
	if got := readWindowHighlights(t, runTmux)[build]; got.marker != "1" {
		t.Fatalf("highlight after focusing %s = %#v, want still highlighted while %s is unseen", workA, got, workB)
	}
	if got := readWindowHighlights(t, runTmux)[other]; got.marker != "1" {
		t.Fatalf("other window = %#v, want another window left marked", got)
	}

	// Focusing the last unseen pane clears the window and unsets both local
	// options, so the custom inherited styles come back.
	focus(workB)
	waitForSeenPane(t, source, ctx, workB)
	waitForWindowMarker(t, runTmux, build, "")
	cleared := readWindowHighlights(t, runTmux)[build]
	if cleared.style != "bg=colour17" || cleared.currentStyle != "bg=colour18" {
		t.Fatalf("cleared build window = %#v, want the inherited global styles", cleared)
	}
	if got := localWindowOption(t, runTmux, build, "window-status-style"); got != "" {
		t.Fatalf("local window-status-style = %q, want unset", got)
	}
	if got := localWindowOption(t, runTmux, build, "window-status-current-style"); got != "" {
		t.Fatalf("local window-status-current-style = %q, want unset", got)
	}
}

// TestTmuxUnseenWindowHighlightWatchShutdownClears pins the orderly shutdown
// cleanup: canceling the watcher clears the highlights it owns while the tmux
// server keeps running.
func TestTmuxUnseenWindowHighlightWatchShutdownClears(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	server := "nexo-test-hl-shutdown-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := throwawayTmux(server)
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	pi := fakeHarness(t)
	if output, err := runTmux("new-session", "-d", "-s", "work", pi+" 600"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}

	source := NewAgentSource(throwawayRunner(server))
	setStateDir(source, t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Watch(ctx) }()

	window := windowByPane(t, runTmux, "work:0.0")
	waitForWindowMarker(t, runTmux, window, "1")
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Watch did not return after cancellation")
	}
	waitForWindowMarker(t, runTmux, window, "")
	if _, err := runTmux("display-message", "-p", "alive"); err != nil {
		t.Fatalf("tmux server should still be running after watcher shutdown: %v", err)
	}
}

// TestTmuxUnseenWindowHighlightVanishedWindow pins MUST 1 against a real
// server: applying to a window killed before the change is a no-op instead of
// an error, for both the set and the clear path.
func TestTmuxUnseenWindowHighlightVanishedWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-hl-vanish-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := throwawayTmux(server)
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	pi := fakeHarness(t)
	if output, err := runTmux("new-session", "-d", "-s", "work", pi+" 600"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}
	if output, err := runTmux("new-window", "-t", "work", "-n", "doomed", pi+" 600"); err != nil {
		t.Fatalf("create window: %v: %s", err, output)
	}
	doomed := windowByPane(t, runTmux, "work:1.0")
	if output, err := runTmux("kill-window", "-t", doomed); err != nil {
		t.Fatalf("kill window: %v: %s", err, output)
	}

	source := NewAgentSource(throwawayRunner(server))
	source.ConfigureAgents(config.AgentsConfig{HighlightColor: "red"})
	if err := source.applyHighlight(ctx, highlightAction{windowID: doomed}, "fg=red"); err != nil {
		t.Fatalf("applyHighlight(set, vanished) error = %v, want nil", err)
	}
	if err := source.applyHighlight(ctx, highlightAction{windowID: doomed, clear: true}, "fg=red"); err != nil {
		t.Fatalf("applyHighlight(clear, vanished) error = %v, want nil", err)
	}
}

type windowHighlightState struct {
	marker       string
	style        string
	currentStyle string
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
// reading the marker and effective styles back with the same format nexo uses.
func readWindowHighlights(t *testing.T, runTmux func(...string) ([]byte, error)) map[string]windowHighlightState {
	t.Helper()
	output, err := runTmux("list-windows", "-a", "-F", windowHighlightFormat)
	if err != nil {
		t.Fatalf("list windows: %v: %s", err, output)
	}
	result := make(map[string]windowHighlightState)
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			continue
		}
		result[fields[0]] = windowHighlightState{
			marker:       strings.TrimSpace(fields[1]),
			style:        strings.TrimSpace(fields[2]),
			currentStyle: strings.TrimSpace(fields[3]),
		}
	}
	return result
}

func localWindowOption(t *testing.T, runTmux func(...string) ([]byte, error), windowID, option string) string {
	t.Helper()
	output, err := runTmux("show-options", "-w", "-t", windowID, "-v", option)
	if err != nil {
		t.Fatalf("show local %s for %s: %v: %s", option, windowID, err, output)
	}
	return strings.TrimSpace(string(output))
}

func waitForWindowMarker(t *testing.T, runTmux func(...string) ([]byte, error), windowID, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if got := readWindowHighlights(t, runTmux)[windowID].marker; got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %s marker never became %q", windowID, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForUnseenAgents(t *testing.T, source *Source, ctx context.Context, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		agents, err := source.listAgents(ctx, true)
		if err != nil {
			t.Fatalf("listAgents() error = %v", err)
		}
		if stoppedUnseen(agents) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agents = %#v, want %d unseen stopped agents", agents, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForSeenPane(t *testing.T, source *Source, ctx context.Context, paneID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		agents, err := source.listAgents(ctx, true)
		if err != nil {
			t.Fatalf("listAgents() error = %v", err)
		}
		for _, agent := range agents {
			if agent.PaneID == paneID && agent.Seen {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never became seen; agents = %#v", paneID, agents)
		}
		time.Sleep(200 * time.Millisecond)
	}
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

// throwawayTmux returns a runner for one throwaway tmux server. Every call
// carries the server flag and ignores user configuration.
func throwawayTmux(server string) func(...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		commandArgs := append([]string{"-L", server, "-f", "/dev/null"}, args...)
		return exec.Command("tmux", commandArgs...).CombinedOutput()
	}
}

// throwawayRunner routes tmux commands to a throwaway server for a Source.
func throwawayRunner(server string) runner.CommandRunner {
	return funcRunner(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = append([]string{"-L", server, "-f", "/dev/null"}, args...)
		}
		return runner.ExecRunner{}.Run(ctx, name, args...)
	})
}

func buildNexoBinary(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "nexo")
	build := exec.Command("go", "build", "-o", binary, "./cmd/nexo")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build nexo: %v\n%s", err, output)
	}
	return binary
}

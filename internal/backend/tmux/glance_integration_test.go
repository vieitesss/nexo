package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/config"
)

// TestTmuxGlanceRow drives a throwaway tmux server to pin the pieces a fake
// runner cannot: the real default `status` value, the extra status row, the
// watcher keeping the row current, status-left staying untouched, and off
// restoring the server's original settings.
func TestTmuxGlanceRow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-glance-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := throwawayTmux(server)
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	pi := fakeHarness(t)
	if output, err := runTmux("new-session", "-d", "-s", "work", pi+" 600"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}
	if output, err := runTmux("set-option", "-g", "status-left", "LEFT"); err != nil {
		t.Fatalf("set status-left: %v: %s", err, output)
	}
	originalStatus := readGlanceOption(t, runTmux, "status")
	originalFormat := readGlanceOption(t, runTmux, "status-format[1]")

	source := NewAgentSource(throwawayRunner(server))
	setStateDir(source, t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}, IdleSeconds: 1, PollInterval: 1})
	source.ConfigureGlance(config.GlanceConfig{Fields: []string{"dot", "target"}})

	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatalf("Glance(on) error = %v", err)
	}
	if got := readGlanceOption(t, runTmux, "status"); got != "2" {
		t.Fatalf("status = %q, want 2", got)
	}
	if got := readGlanceOption(t, runTmux, "status-left"); got != "LEFT" {
		t.Fatalf("status-left = %q, want LEFT untouched", got)
	}

	// A watcher poll must keep the row current as the agent appears.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := source.listAgents(ctx, true); err != nil {
			t.Fatalf("listAgents() error = %v", err)
		}
		if row := readGlanceOption(t, runTmux, "status-format[1]"); strings.Contains(row, "work:0.0") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("row never listed the agent: %q", readGlanceOption(t, runTmux, "status-format[1]"))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// tmux must accept the stored value as a format and render it.
	rendered, err := runTmux("display-message", "-p", readGlanceOption(t, runTmux, "status-format[1]"))
	if err != nil {
		t.Fatalf("render glance row: %v: %s", err, rendered)
	}
	if !strings.Contains(string(rendered), "nexo │ [") {
		t.Fatalf("rendered row = %q, want the glance label and an entry", rendered)
	}

	if err := source.Glance(ctx, GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if got := readGlanceOption(t, runTmux, "status"); got != originalStatus {
		t.Fatalf("status after off = %q, want %q", got, originalStatus)
	}
	if got := readGlanceOption(t, runTmux, "status-format[1]"); got != originalFormat {
		t.Fatalf("status-format[1] after off = %q, want %q", got, originalFormat)
	}
	if got := readGlanceOption(t, runTmux, "status-left"); got != "LEFT" {
		t.Fatalf("status-left after off = %q, want LEFT untouched", got)
	}
}

// TestTmuxGlancePreservesACustomSecondRow pins that enabling glance with a
// two-line status bar takes a third row instead of overwriting the user's.
func TestTmuxGlancePreservesACustomSecondRow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	server := "nexo-test-glance-custom-" + strings.ReplaceAll(t.Name(), "/", "-")
	runTmux := throwawayTmux(server)
	t.Cleanup(func() { _, _ = runTmux("kill-server") })

	pi := fakeHarness(t)
	if output, err := runTmux("new-session", "-d", "-s", "work", pi+" 600"); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}
	if output, err := runTmux("set-option", "-g", "status", "2"); err != nil {
		t.Fatalf("set status: %v: %s", err, output)
	}
	if output, err := runTmux("set-option", "-g", "status-format[1]", "USER-ROW"); err != nil {
		t.Fatalf("set custom row: %v: %s", err, output)
	}

	source := NewAgentSource(throwawayRunner(server))
	setStateDir(source, t.TempDir())
	source.ConfigureAgents(config.AgentsConfig{Processes: []string{"pi"}})
	if err := source.Glance(ctx, GlanceOn); err != nil {
		t.Fatalf("Glance(on) error = %v", err)
	}
	if got := readGlanceOption(t, runTmux, "status"); got != "3" {
		t.Fatalf("status = %q, want 3", got)
	}
	if got := readGlanceOption(t, runTmux, "status-format[1]"); got != "USER-ROW" {
		t.Fatalf("status-format[1] = %q, want the custom row preserved", got)
	}
	if got := readGlanceOption(t, runTmux, "status-format[2]"); !strings.HasPrefix(got, "nexo │") {
		t.Fatalf("status-format[2] = %q, want the glance row", got)
	}

	if err := source.Glance(ctx, GlanceOff); err != nil {
		t.Fatalf("Glance(off) error = %v", err)
	}
	if got := readGlanceOption(t, runTmux, "status"); got != "2" {
		t.Fatalf("status after off = %q, want 2", got)
	}
	if got := readGlanceOption(t, runTmux, "status-format[1]"); got != "USER-ROW" {
		t.Fatalf("status-format[1] after off = %q, want the custom row", got)
	}
	if got := readGlanceOption(t, runTmux, "status-format[2]"); got != "" {
		t.Fatalf("status-format[2] after off = %q, want unset", got)
	}
}

func readGlanceOption(t *testing.T, runTmux func(...string) ([]byte, error), name string) string {
	t.Helper()
	output, err := runTmux("show-options", "-gqv", name)
	if err != nil {
		t.Fatalf("read option %s: %v: %s", name, err, output)
	}
	return strings.TrimSpace(string(output))
}

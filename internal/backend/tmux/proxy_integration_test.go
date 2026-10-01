package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

type isolatedTmuxRunner struct {
	socket string
}

func (r isolatedTmuxRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "tmux" {
		return runner.ExecRunner{}.Run(ctx, name, args...)
	}
	args = append([]string(nil), args...)
	operation := args[0]
	if operation == "-u" && len(args) > 1 {
		operation = args[1]
	}
	if operation == "new-session" {
		// Keep this integration test from launching the generated hidden attach
		// command; the session-name parser is the behavior under test.
		args[len(args)-1] = "sleep 600"
	}
	return runner.ExecRunner{}.Run(ctx, "tmux", append([]string{"-L", r.socket, "-f", "/dev/null"}, args...)...)
}

func (r isolatedTmuxRunner) RunInteractive(context.Context, string, ...string) error {
	return nil
}

func TestUnadoptedRemoteNamedSessionsAreProxiesOnThrowawayTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	socket := "nexo-test-adopt-" + strings.ReplaceAll(t.Name(), "/", "-")
	r := isolatedTmuxRunner{socket: socket}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.Run(ctx, "tmux", "kill-server")
	})
	for _, name := range []string{"vieitesrpi/foo", "notamachine/foo"} {
		if _, err := r.Run(ctx, "tmux", "new-session", "-d", "-s", name, "sleep 600"); err != nil {
			t.Fatalf("create session %q: %v", name, err)
		}
	}
	backend := New(r, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{" VIEITESRPI "}}, "")
	containers, err := backend.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers(): %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("containers = %#v, want proxy and local session", containers)
	}
	var proxy, local capabilities.Container
	for _, container := range containers {
		if container.Name == "foo" && container.Proxy {
			proxy = container
		} else if container.Name == "notamachine/foo" && !container.Proxy {
			local = container
		}
	}
	if proxy.ID == "" || proxy.Machine != "vieitesrpi" {
		t.Fatalf("proxy = %#v, want vieitesrpi/foo proxy", proxy)
	}
	if local.ID == "" {
		t.Fatalf("containers = %#v, want unknown-machine session to remain local", containers)
	}
	if err := backend.Proxy(ctx, "vieitesrpi", "foo"); err != nil {
		t.Fatalf("adopt existing session: %v", err)
	}
	// Adoption must write a session-local option only: the global table stays
	// clean and the unrelated session must not inherit anything.
	if value, ok := showTmuxOption(ctx, t, r, true, "", "@nexo-proxy"); ok && value != "" {
		t.Fatalf("global @nexo-proxy = %q, want unset", value)
	}
	if value, ok := showTmuxOption(ctx, t, r, false, local.ID, "@nexo-proxy"); ok && value != "" {
		t.Fatalf("unrelated session @nexo-proxy = %q, want unset", value)
	}
	if value, ok := showTmuxOption(ctx, t, r, false, proxy.ID, "@nexo-proxy"); !ok || value != "vieitesrpi/foo" {
		t.Fatalf("target session-local @nexo-proxy = %q (set=%v), want vieitesrpi/foo", value, ok)
	}
}

// showTmuxOption reads an option without tmux's fallback semantics: with
// global it reads the global table, otherwise the target's session-local
// value. ok is false when the option is unset.
func showTmuxOption(ctx context.Context, t *testing.T, r isolatedTmuxRunner, global bool, target, option string) (string, bool) {
	t.Helper()
	args := []string{"show-options", "-t", target, "-v", option}
	if global {
		args = []string{"show-options", "-gv", option}
	}
	output, err := r.Run(ctx, "tmux", args...)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(output)), true
}

func TestConflictingProxyTagIsNeverAdoptedOnThrowawayTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	socket := "nexo-test-conflict-" + strings.ReplaceAll(t.Name(), "/", "-")
	r := isolatedTmuxRunner{socket: socket}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.Run(ctx, "tmux", "kill-server")
	})
	if _, err := r.Run(ctx, "tmux", "new-session", "-d", "-s", "vieitesrpi/foo", "sleep 600"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := r.Run(ctx, "tmux", "set-option", "-t", "vieitesrpi/foo", "@nexo-proxy", "vieitesrpi/bar"); err != nil {
		t.Fatalf("tag session: %v", err)
	}
	backend := New(r, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"vieitesrpi"}}, "")

	id, err := backend.ProxySessionID(ctx, "vieitesrpi", "foo")
	if err != nil {
		t.Fatalf("ProxySessionID(vieitesrpi, foo): %v", err)
	}
	if id != "" {
		t.Fatalf("ProxySessionID(vieitesrpi, foo) = %q, want no adoption of a conflicting tag", id)
	}
	if value, ok := showTmuxOption(ctx, t, r, false, "vieitesrpi/foo", "@nexo-proxy"); !ok || value != "vieitesrpi/bar" {
		t.Fatalf("tag = %q (set=%v), want vieitesrpi/bar: the tag must not be overwritten", value, ok)
	}
	// A tag naming another identity wins over the display name when listing.
	containers, err := backend.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers(): %v", err)
	}
	if len(containers) != 1 || !containers[0].Proxy || containers[0].Machine != "vieitesrpi" || containers[0].Name != "bar" {
		t.Fatalf("containers = %#v, want the conflicting tag classified as vieitesrpi/bar", containers)
	}
	// The tag remains reachable under the identity it names.
	tagged, err := backend.ProxySessionID(ctx, "vieitesrpi", "bar")
	if err != nil {
		t.Fatalf("ProxySessionID(vieitesrpi, bar): %v", err)
	}
	if tagged == "" {
		t.Fatal("ProxySessionID(vieitesrpi, bar) = \"\", want the tagged session")
	}
}

func TestProxyUsesExactMetadataIdentityOnThrowawayTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is unavailable")
	}
	socket := "nexo-test-proxy-" + strings.ReplaceAll(t.Name(), "/", "-")
	r := isolatedTmuxRunner{socket: socket}
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.Run(ctx, "tmux", "kill-server")
	})

	marker := filepath.Join(t.TempDir(), "format-job-ran")
	backend := New(r, false)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "")
	for _, target := range [][2]string{
		{"devbox", "prod"},
		{"devbox", "prod-long"},
		{"dev.box:edge", "app.v1:blue"},
		{"devbox", "hash#(touch " + marker + ")"},
		{"devbox", "format#{session_name}"},
	} {
		if err := backend.Proxy(ctx, target[0], target[1]); err != nil {
			t.Fatalf("Proxy(%q, %q): %v", target[0], target[1], err)
		}
	}
	if err := backend.Proxy(ctx, "devbox", "prod"); err != nil {
		t.Fatalf("reopening existing proxy: %v", err)
	}
	if _, err := backend.ProxySessionID(ctx, "devbox", "prod"); err != nil {
		t.Fatalf("find existing proxy: %v", err)
	}
	if err := backend.Proxy(ctx, "devbox", "unsafe;"); err == nil {
		t.Fatal("Proxy() accepted a trailing semicolon")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("format job marker exists: %v", err)
	}

	output, err := r.Run(ctx, "tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{@nexo-proxy}")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 5 {
		t.Fatalf("sessions = %q, want five exact proxy identities", output)
	}
	if !strings.Contains(string(output), "dev.box:edge/app_v1_blue") {
		t.Fatalf("metadata did not use canonical container name: %q", output)
	}
	identities := make(map[string]bool)
	for _, line := range lines {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) == 3 {
			identities[fields[2]] = true
		}
	}
	if !identities["devbox/prod"] || !identities["devbox/prod-long"] {
		t.Fatalf("prefix identities were not kept distinct: %q", output)
	}
}

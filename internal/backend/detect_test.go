package backend

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner func(context.Context, string, ...string) ([]byte, error)

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

type mapEnvironment map[string]string

func (m mapEnvironment) LookupEnv(key string) (string, bool) {
	value, ok := m[key]
	return value, ok
}

func TestDetectHerdrFromEnvironment(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{"HERDR_ENV": "1"}, "/dev/pts/4", nil)
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Herdr || !strings.Contains(reason, "HERDR_ENV") {
		t.Fatalf("detect() = %q, %q, want Herdr detected from HERDR_ENV", kind, reason)
	}
}

func TestDetectTmuxFromEnvironment(t *testing.T) {
	kind, _, err := detect(context.Background(), mapEnvironment{"TMUX": "/tmp/tmux", "TMUX_PANE": "%1"}, "/dev/pts/4", nil)
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Tmux {
		t.Fatalf("detect() = %q, want tmux", kind)
	}
}

func TestDetectNestedUsesTTYOwnedByTmux(t *testing.T) {
	kind, _, err := detect(context.Background(), mapEnvironment{
		"HERDR_ENV": "1",
		"TMUX":      "/tmp/tmux",
	}, "/dev/pts/4", fakeRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			t.Fatalf("command = %q, want tmux", name)
		}
		if strings.Contains(strings.Join(args, " "), "list-clients") {
			return []byte("/dev/pts/4\n"), nil
		}
		return []byte("/dev/pts/9\n"), nil
	}))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Tmux {
		t.Fatalf("detect() = %q, want tmux", kind)
	}
}

func TestDetectNestedFallsBackToHerdrWhenTTYNotOwnedByTmux(t *testing.T) {
	kind, _, err := detect(context.Background(), mapEnvironment{
		"HERDR_PANE_ID": "pane-1",
		"TMUX_PANE":     "%1",
	}, "/dev/pts/4", fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("/dev/pts/9\n"), nil
	}))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Herdr {
		t.Fatalf("detect() = %q, want Herdr", kind)
	}
}

func TestDetectOutsideMultiplexer(t *testing.T) {
	kind, _, err := detect(context.Background(), mapEnvironment{}, "/dev/pts/4", nil)
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != None {
		t.Fatalf("detect() = %q, want no backend", kind)
	}
}

func TestDetectNestedReportsRunnerFailure(t *testing.T) {
	want := errors.New("tmux unavailable")
	_, _, err := detect(context.Background(), mapEnvironment{"HERDR_ENV": "1", "TMUX": "yes"}, "/dev/pts/4", fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return nil, want
	}))
	if err == nil || !strings.Contains(err.Error(), "tmux unavailable") {
		t.Fatalf("error = %v, want runner failure", err)
	}
}

func TestDetectRexFromVerifiedSessionMarker(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
	}, "/dev/ttys010", detectionProbes("ttys010", ""))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Rex || !strings.Contains(reason, "REX_SESSION") {
		t.Fatalf("detect() = %q, %q, want Rex detected from a verified REX_SESSION", kind, reason)
	}
	if !strings.Contains(reason, "controlling TTY") {
		t.Fatalf("reason = %q, want the TTY ownership that confirmed Rex stated", reason)
	}
}

func TestDetectRexFallsBackWhenTheSessionMarkerIsStale(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{"REX_SESSION": "session:stale"}, "/dev/pts/4", fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"host_id":"host-1"}`), nil
	}))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != None {
		t.Fatalf("detect() = %q, want no backend for a stale REX_SESSION", kind)
	}
	if !strings.Contains(reason, "REX_SESSION") {
		t.Fatalf("reason = %q, want the stale marker named", reason)
	}
}

func TestDetectTmuxInsideRexIsTheInnerMultiplexer(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
		"TMUX":        "/tmp/tmux",
		"TMUX_PANE":   "%1",
	}, "/dev/ttys009", detectionProbes("ttys010", "/dev/ttys009"))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Tmux || !strings.Contains(reason, "inside a Rex block") {
		t.Fatalf("detect() = %q, %q, want tmux as the inner multiplexer", kind, reason)
	}
}

func TestDetectHerdrInsideRexIsTheInnerMultiplexer(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
		"HERDR_ENV":   "1",
	}, "/dev/ttys009", detectionProbes("ttys010", ""))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Herdr || !strings.Contains(reason, "inside a Rex block") {
		t.Fatalf("detect() = %q, %q, want Herdr as the inner multiplexer", kind, reason)
	}
}

// detectionProbes serves the ownership probes: a live Rex whoami, the block's
// child and foreground pids, ps for their terminal, and tmux's pane list.
// blockTTY is what ps reports for the block process; tmuxTTY is what tmux
// reports for its panes. An empty tmuxTTY fails the probe, which proves the
// decision never consulted tmux.
func detectionProbes(blockTTY, tmuxTTY string) fakeRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		switch {
		case filepath.Base(name) == "rex" && line == "--autostart=false whoami --json":
			return []byte(`{"session_id":"session:1"}`), nil
		case filepath.Base(name) == "rex" && strings.HasPrefix(line, "--autostart=false api call block.method "):
			return []byte(`{"child":{"pid":42},"foreground":{"pid":43}}`), nil
		case name == "ps":
			return []byte(blockTTY + "\n"), nil
		case name == "tmux" && tmuxTTY != "":
			return []byte(tmuxTTY + "\n"), nil
		}
		return nil, fmt.Errorf("unexpected command %s %s", name, line)
	}
}

func TestDetectRexWinsWhenTheBlockOwnsTheControllingTTY(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"TMUX":        "/tmp/tmux",
		"TMUX_PANE":   "%1",
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
	}, "/dev/ttys010", detectionProbes("ttys010", ""))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Rex {
		t.Fatalf("detect() = %q, %q, want Rex: its block owns the controlling TTY", kind, reason)
	}
	if !strings.Contains(reason, "REX_SESSION") || !strings.Contains(reason, "inherited") {
		t.Fatalf("reason = %q, want the inherited tmux marker explained", reason)
	}
}

func TestDetectRejectsALiveRexSessionInheritedOutsideRex(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
	}, "/dev/ttys009", detectionProbes("ttys010", ""))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != None {
		t.Fatalf("detect() = %q, %q, want no backend: the live id is inherited", kind, reason)
	}
	if !strings.Contains(reason, "controlling TTY") || !strings.Contains(reason, "--backend") {
		t.Fatalf("reason = %q, want the ownership mismatch and the explicit escape hatch", reason)
	}
}

func TestDetectKeepsMarkerPrecedenceWithoutAControllingTTY(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"TMUX":        "/tmp/tmux",
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
	}, "", detectionProbes("??", ""))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != Tmux {
		t.Fatalf("detect() = %q, %q, want today's tmux precedence without a TTY", kind, reason)
	}
	if !strings.Contains(reason, "no controlling TTY") {
		t.Fatalf("reason = %q, want the missing controlling TTY stated", reason)
	}
}

func TestDetectReportsDisagreeingMarkersWhenNeitherOwnsTheTTY(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"TMUX":        "/tmp/tmux",
		"REX_SESSION": "session:1",
		"REX_BLOCK":   "block:1",
	}, "/dev/ttys009", detectionProbes("ttys010", "/dev/ttys008"))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != None {
		t.Fatalf("detect() = %q, %q, want no guess between disagreeing markers", kind, reason)
	}
	if !strings.Contains(reason, "--backend") {
		t.Fatalf("reason = %q, want the explicit escape hatch", reason)
	}
}

func TestDetectReportsAnUnverifiableRexBlock(t *testing.T) {
	kind, reason, err := detect(context.Background(), mapEnvironment{
		"TMUX":        "/tmp/tmux",
		"REX_SESSION": "session:1",
	}, "/dev/ttys010", detectionProbes("ttys010", ""))
	if err != nil {
		t.Fatalf("detect() error = %v", err)
	}
	if kind != None || !strings.Contains(reason, "--backend") {
		t.Fatalf("detect() = %q, %q, want an explicit request for --backend when ownership is unknown", kind, reason)
	}
}

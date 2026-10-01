package backend

import (
	"context"
	"errors"
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

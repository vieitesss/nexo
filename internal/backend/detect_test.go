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

func TestDetectHerdrFromEnvironment(t *testing.T) {
	selection, err := Detect(context.Background(), MapEnvironment{"HERDR_ENV": "1"}, "/dev/pts/4", nil)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if selection.Kind != Herdr || !strings.Contains(selection.Reason, "HERDR_ENV") {
		t.Fatalf("selection = %#v, want Herdr detected from HERDR_ENV", selection)
	}
}

func TestDetectTmuxFromEnvironment(t *testing.T) {
	selection, err := Detect(context.Background(), MapEnvironment{"TMUX": "/tmp/tmux", "TMUX_PANE": "%1"}, "/dev/pts/4", nil)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if selection.Kind != Tmux {
		t.Fatalf("selection = %#v, want tmux", selection)
	}
}

func TestDetectNestedUsesTTYOwnedByTmux(t *testing.T) {
	selection, err := Detect(context.Background(), MapEnvironment{
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
		t.Fatalf("Detect() error = %v", err)
	}
	if selection.Kind != Tmux {
		t.Fatalf("selection = %#v, want tmux", selection)
	}
}

func TestDetectNestedFallsBackToHerdrWhenTTYNotOwnedByTmux(t *testing.T) {
	selection, err := Detect(context.Background(), MapEnvironment{
		"HERDR_PANE_ID": "pane-1",
		"TMUX_PANE":     "%1",
	}, "/dev/pts/4", fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("/dev/pts/9\n"), nil
	}))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if selection.Kind != Herdr {
		t.Fatalf("selection = %#v, want Herdr", selection)
	}
}

func TestDetectOutsideMultiplexer(t *testing.T) {
	selection, err := Detect(context.Background(), MapEnvironment{}, "/dev/pts/4", nil)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if selection.Kind != None {
		t.Fatalf("selection = %#v, want no backend", selection)
	}
}

func TestDetectNestedReportsRunnerFailure(t *testing.T) {
	want := errors.New("tmux unavailable")
	_, err := Detect(context.Background(), MapEnvironment{"HERDR_ENV": "1", "TMUX": "yes"}, "/dev/pts/4", fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return nil, want
	}))
	if err == nil || !strings.Contains(err.Error(), "tmux unavailable") {
		t.Fatalf("error = %v, want runner failure", err)
	}
}

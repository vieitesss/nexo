package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type notifierCall struct {
	name string
	args []string
}

type notifierRunner struct {
	calls []notifierCall
	err   error
}

func (r *notifierRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, notifierCall{name: name, args: append([]string(nil), args...)})
	return nil, r.err
}

func TestNotifierRateLimitsAcrossPanes(t *testing.T) {
	runner := &notifierRunner{}
	notifier := NewNotifier(runner)
	notifier.goos = "linux"
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	notifier.now = func() time.Time { return now }
	notifier.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	for _, message := range []string{"pi stopped in work:1.1", "claude stopped in work:1.2"} {
		if err := notifier.Notify(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.calls) != 1 {
		t.Fatalf("notification calls = %#v, want one call within three seconds", runner.calls)
	}
	if runner.calls[0].name != "notify-send" || !strings.Contains(strings.Join(runner.calls[0].args, " "), "pi stopped in work:1.1") {
		t.Fatalf("notification call = %#v, want notify-send with first message", runner.calls[0])
	}

	now = now.Add(3 * time.Second)
	if err := notifier.Notify(context.Background(), "claude stopped in work:1.2"); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("notification calls = %#v, want second call after rate limit", runner.calls)
	}
}

func TestNotifierUsesDarwinCommands(t *testing.T) {
	runner := &notifierRunner{}
	notifier := NewNotifier(runner)
	notifier.goos = "darwin"
	notifier.sound = true
	notifier.lookPath = func(string) (string, error) { return "/usr/bin/tool", nil }
	if err := notifier.Notify(context.Background(), "pi stopped"); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "osascript" || runner.calls[1].name != "afplay" {
		t.Fatalf("calls = %#v, want osascript and afplay", runner.calls)
	}
}

func TestNotifierSkipsMissingTools(t *testing.T) {
	runner := &notifierRunner{}
	notifier := NewNotifier(runner)
	notifier.goos = "linux"
	notifier.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := notifier.Notify(context.Background(), "pi stopped"); err != nil {
		t.Fatalf("Notify() error = %v, want no error when tool is absent", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("notification calls = %#v, want no command when tool is absent", runner.calls)
	}
}

func TestNotifierSoundIsOptionalAndBestEffort(t *testing.T) {
	runner := &notifierRunner{}
	notifier := NewNotifier(runner)
	notifier.goos = "linux"
	notifier.sound = true
	notifier.now = func() time.Time { return time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC) }
	notifier.lookPath = func(name string) (string, error) {
		if name == "canberra-gtk-play" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	if err := notifier.Notify(context.Background(), "pi stopped"); err != nil {
		t.Fatalf("Notify() error = %v, want sound failure to stay best effort", err)
	}
	if len(runner.calls) != 2 || runner.calls[0].name != "notify-send" || runner.calls[1].name != "paplay" {
		t.Fatalf("calls = %#v, want notification and paplay sound", runner.calls)
	}
}

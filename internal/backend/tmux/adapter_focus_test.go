package tmux

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

type focusEventsRunner struct {
	fakeRunner
}

func (r *focusEventsRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" && len(args) == 3 && args[0] == "show-option" {
		r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
		return []byte("on\n"), nil
	}
	return r.fakeRunner.Run(ctx, name, args...)
}

func TestInitLeavesExistingFocusEventsEnabled(t *testing.T) {
	runner := &focusEventsRunner{}
	backend := New(runner, true)
	if err := backend.Init(context.Background(), InitInput{Config: config.TmuxConfig{}}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if len(current.args) >= 2 && current.args[0] == "set-option" && current.args[len(current.args)-1] == "on" {
			t.Fatalf("calls = %#v, want no focus-events change when already on", runner.calls)
		}
	}
}

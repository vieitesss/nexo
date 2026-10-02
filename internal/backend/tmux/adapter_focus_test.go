package tmux

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestInitLeavesExistingFocusEventsEnabled(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"show-option -gqv focus-events": []byte("on\n"),
	}}
	if err := New(runner, true).Init(context.Background(), capabilities.InitInput{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, current := range runner.calls {
		if len(current.args) >= 2 && current.args[1] == "set-option" && current.args[len(current.args)-1] == "on" {
			t.Fatalf("calls = %#v, want no focus-events change when already on", runner.calls)
		}
	}
}

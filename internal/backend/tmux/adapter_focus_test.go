package tmux

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestInitLeavesExistingFocusEventsEnabled(t *testing.T) {
	placeLine := "bind-key -T root C-s display-popup -E -h 100% -w 100% -x C -y C \"" + pickerCommand("") + "\"\n"
	agentLine := "bind-key -T prefix a display-popup -E -h 100% -w 100% -x C -y C \"" + agentPickerCommand("") + "\"\n"
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"show-option -gqv focus-events": []byte("on\n"),
		},
		sequences: map[string][][]byte{
			"list-keys -T root C-s": {nil, []byte(placeLine)},
			"list-keys -T prefix a": {nil, []byte(agentLine)},
		},
	}
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

package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type focusCommand struct{}

func init() {
	registerBuiltin(func() Command { return focusCommand{} })
}

func (focusCommand) Name() string { return "focus" }

// Run records the container named by a backend focus event. It is the command
// declared by the Herdr plugin's [[events]] hook; tmux records focus through
// its own hooks.
func (focusCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	if len(invocation.Args) != 0 {
		return errors.New("focus accepts no arguments")
	}
	recorder, ok := invocation.Adapter.(capabilities.FocusEventRecorder)
	if !ok {
		return fmt.Errorf("backend %q does not support focus events", invocation.Selection.Kind)
	}
	return recorder.RecordFocusEvent(ctx, []byte(os.Getenv("HERDR_PLUGIN_EVENT_JSON")))
}

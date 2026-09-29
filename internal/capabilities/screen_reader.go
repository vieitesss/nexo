package capabilities

import "context"

// ScreenReader reads the current screen of a pane.
type ScreenReader interface {
	ReadScreen(context.Context, string) (string, error)
}

package capabilities

import "context"

// MessageDisplayer shows a short status message in the multiplexer's own UI,
// for key-bound commands whose stderr the user never sees.
type MessageDisplayer interface {
	DisplayMessage(context.Context, string) error
}

package capabilities

import "context"

// PaneFocuser focuses a pane.
type PaneFocuser interface {
	FocusPane(context.Context, string) error
}

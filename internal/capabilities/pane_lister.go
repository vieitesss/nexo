package capabilities

import "context"

// PaneLister lists panes in the active backend.
type PaneLister interface {
	ListPanes(context.Context) ([]Pane, error)
}

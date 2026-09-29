package capabilities

import "context"

// Popup shows a backend popup.
type Popup interface {
	ShowPopup(context.Context, string) error
}

package capabilities

import "context"

// Notifier sends a notification through the backend.
type Notifier interface {
	Notify(context.Context, string) error
}

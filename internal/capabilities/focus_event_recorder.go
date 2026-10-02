package capabilities

import "context"

// FocusEventRecorder persists the container that gained focus in the backend's
// native focus history. The payload is backend-native event data; backends
// that identify the focused container from their environment may ignore it.
type FocusEventRecorder interface {
	RecordFocusEvent(context.Context, []byte) error
}

package capabilities

import "context"

// RemoteProxy opens or focuses the one machine proxy. A non-empty container
// explicitly targets that remote session; an empty one only switches local focus.
type RemoteProxy interface {
	Proxy(context.Context, string, string) error
}

package capabilities

import "context"

// RemoteProxy opens a proxy to a container on another machine.
type RemoteProxy interface {
	Proxy(context.Context, string, string) error
}

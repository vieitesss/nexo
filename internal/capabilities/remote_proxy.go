package capabilities

import "context"

// RemoteProxy opens or focuses the one machine proxy for a remote machine. A
// non-empty container explicitly targets that remote container; an empty one
// only switches local focus.
//
// remoteKind names the multiplexer running on the remote machine, which is not
// always the local backend's: the proxy container stays local and its pane runs
// the remote multiplexer's own client. A backend attaches to its own kind
// natively and to any other kind through that pane, so a backend that cannot
// bridge a kind rejects it instead of guessing.
type RemoteProxy interface {
	Proxy(context.Context, string, string, string) error
}

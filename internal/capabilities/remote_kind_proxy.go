package capabilities

import "context"

// RemoteKindProxy opens or focuses the one machine proxy for a remote machine
// whose multiplexer is not the local backend's. The proxy container stays
// local; only its pane attaches to the remote multiplexer named by kind, so a
// backend never has to drive another multiplexer directly.
type RemoteKindProxy interface {
	ProxyRemote(context.Context, string, string, string) error
}

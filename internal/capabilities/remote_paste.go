package capabilities

import "context"

// RemotePaste is the backend surface used by the remote paste command. The
// backend resolves only the local proxy metadata and injects text into its
// attached pane; it never opens or closes the remote container.
type RemotePaste interface {
	ProxyTarget(context.Context, string) (string, error)
	SendPaneKeys(context.Context, string, []string) error
	SendPaneText(context.Context, string, string) error
}

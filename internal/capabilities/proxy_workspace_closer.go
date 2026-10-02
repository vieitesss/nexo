package capabilities

import "context"

// ProxyWorkspaceCloser closes the local native workspace that owns a proxy
// pane after the proxy attachment exits.
type ProxyWorkspaceCloser interface {
	CloseProxyWorkspace(context.Context, string) error
}

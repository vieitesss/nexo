package capabilities

import "context"

// ProxySessionResolver finds the local container ID for a proxy identity.
// The returned ID belongs to the local backend; resolving it never contacts
// the remote machine.
type ProxySessionResolver interface {
	ProxySessionID(context.Context, string, string) (string, error)
}

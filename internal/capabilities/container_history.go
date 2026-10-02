package capabilities

import (
	"context"

	"github.com/vieitesss/nexo/internal/containerstate"
)

// ContainerHistory reads the durable focus order for one backend instance.
type ContainerHistory interface {
	RecentContainers(context.Context) ([]containerstate.Entry, error)
}

// CurrentContainer reports the container owning the caller's pane.
type CurrentContainer interface {
	CurrentContainerID(context.Context) (string, error)
}

// CurrentProxyMachine reports the remote machine attached by the caller's
// proxy, or an empty string when the caller is not inside a proxy.
type CurrentProxyMachine interface {
	CurrentProxyMachine(context.Context) (string, error)
}

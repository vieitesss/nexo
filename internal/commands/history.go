package commands

import (
	"context"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

// containerHistory is implemented by backends that persist recent container
// identities scoped to their native server instance.
type containerHistory interface {
	RecentContainers(context.Context) ([]containerstate.Entry, error)
}

// currentContainer identifies the container owning the calling pane. An empty
// result means the caller is not inside a container.
type currentContainer interface {
	CurrentContainerID(context.Context) (string, error)
}

// currentProxy identifies the remote machine attached by the calling pane. An
// empty result means the caller is not inside a proxy.
type currentProxy interface {
	CurrentProxyMachine(context.Context) (string, error)
}

// liveLocalContainerIDs lists the native IDs of non-proxy containers that
// currently exist, so previous never selects a killed container.
func liveLocalContainerIDs(ctx context.Context, invocation Invocation) (map[string]struct{}, error) {
	lister, ok := invocation.Adapter.(capabilities.ContainerLister)
	if !ok {
		return nil, fmt.Errorf("backend %q does not support listing containers", invocation.Selection.Kind)
	}
	containers, err := lister.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		if container.Proxy || container.ID == "" {
			continue
		}
		ids[container.ID] = struct{}{}
	}
	return ids, nil
}

func currentContainerID(ctx context.Context, invocation Invocation) string {
	source, ok := invocation.Adapter.(currentContainer)
	if !ok {
		return ""
	}
	id, err := source.CurrentContainerID(ctx)
	if err != nil {
		return ""
	}
	return id
}

func currentProxyMachine(ctx context.Context, invocation Invocation) string {
	source, ok := invocation.Adapter.(currentProxy)
	if !ok {
		return ""
	}
	machine, err := source.CurrentProxyMachine(ctx)
	if err != nil {
		return ""
	}
	return machine
}

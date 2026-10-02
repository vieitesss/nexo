package commands

import (
	"context"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

func recentContainerEntries(ctx context.Context, invocation Invocation) ([]containerstate.Entry, error) {
	history, ok := invocation.Adapter.(capabilities.ContainerHistory)
	if !ok {
		return nil, fmt.Errorf("backend %q does not support container history", invocation.Selection.Kind)
	}
	return history.RecentContainers(ctx)
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

func currentContainerID(ctx context.Context, invocation Invocation) (string, error) {
	source, ok := invocation.Adapter.(capabilities.CurrentContainer)
	if !ok {
		return "", nil
	}
	return source.CurrentContainerID(ctx)
}

func currentProxyMachine(ctx context.Context, invocation Invocation) (string, error) {
	source, ok := invocation.Adapter.(capabilities.CurrentProxyMachine)
	if !ok {
		return "", nil
	}
	return source.CurrentProxyMachine(ctx)
}

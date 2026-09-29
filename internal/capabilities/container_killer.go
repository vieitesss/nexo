package capabilities

import "context"

// ContainerKiller closes one container. The caller is responsible for
// obtaining one confirmation before invoking it for a selection.
type ContainerKiller interface {
	KillContainer(context.Context, string) error
}

// ContainerBatchKiller is an optional native bulk-close capability. It lets a
// backend perform one operation for a complete confirmed selection while the
// singular capability remains sufficient for every backend.
type ContainerBatchKiller interface {
	KillContainers(context.Context, []string) error
}

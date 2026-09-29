package capabilities

import "context"

// ContainerRenamer changes one container's display name.
type ContainerRenamer interface {
	RenameContainer(context.Context, string, string) error
}

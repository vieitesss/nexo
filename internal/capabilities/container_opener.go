package capabilities

import "context"

// ContainerOpener focuses an existing backend container.
type ContainerOpener interface {
	OpenContainer(context.Context, string) error
}

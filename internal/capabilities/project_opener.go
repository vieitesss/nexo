package capabilities

import "context"

// OpenOptions controls backend-specific project opening behavior.
type OpenOptions struct {
	// ContainerID selects an existing container when supported by the backend.
	ContainerID string
}

// ProjectOpener opens a project in a container.
type ProjectOpener interface {
	OpenProject(context.Context, string, OpenOptions) error
}

package capabilities

import "context"

// OpenOptions controls backend-specific project opening behavior.
type OpenOptions struct {
	// ContainerID selects an existing container when supported by the backend.
	ContainerID string
	// NoFocus asks the backend to create or reuse the container without moving
	// the user's view to it.
	NoFocus bool
}

// OpenedContainer identifies the container a project open created or reused.
type OpenedContainer struct {
	ID      string
	Name    string
	Path    string
	Created bool
}

// ProjectOpener opens a project in a container and reports that container.
type ProjectOpener interface {
	OpenProject(context.Context, string, OpenOptions) (OpenedContainer, error)
}

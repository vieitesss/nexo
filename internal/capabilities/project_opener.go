package capabilities

import "context"

// ProjectOpener opens a project in a container.
type ProjectOpener interface {
	OpenProject(context.Context, string) error
}

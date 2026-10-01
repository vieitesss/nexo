package capabilities

import "context"

// ProjectUseChecker is the explicit read-only safety capability used before a
// project is removed. Implementations must report an error when they cannot
// establish whether a live container uses the path; unknown is never idle.
type ProjectUseChecker interface {
	ProjectActive(context.Context, string) (bool, error)
}

package capabilities

import "context"

// WorktreeCreateOptions controls creation of a Git worktree.
type WorktreeCreateOptions struct {
	Repo        string
	Branch      string
	Destination string
	NewBranch   bool
	// NoFocus asks the backend to open the worktree container without moving
	// the user's view to it.
	NoFocus bool
}

// Worktree is the result of creating a Git worktree.
type Worktree struct {
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	Repo        string `json:"-"`
	NewBranch   bool   `json:"-"`
	ContainerID string `json:"-"`
	SessionName string `json:"-"`
	// Container is the container the creator opened for the worktree.
	Container OpenedContainer `json:"-"`
}

// WorktreeCreator creates a worktree and, when appropriate, opens its
// container in the selected backend.
type WorktreeCreator interface {
	CreateWorktree(context.Context, WorktreeCreateOptions) (Worktree, error)
}

// WorktreeRemover rolls back a worktree created by this invocation.
type WorktreeRemover interface {
	RemoveWorktree(context.Context, Worktree) error
}

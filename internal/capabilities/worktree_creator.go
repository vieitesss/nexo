package capabilities

import "context"

// WorktreeCreateOptions controls creation of a Git worktree.
type WorktreeCreateOptions struct {
	Repo        string
	Branch      string
	Destination string
	NewBranch   bool
}

// Worktree is the result of creating a Git worktree.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// WorktreeCreator creates a worktree and, when appropriate, opens its
// container in the selected backend.
type WorktreeCreator interface {
	CreateWorktree(context.Context, WorktreeCreateOptions) (Worktree, error)
}

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
	// ReturnBranch is the branch an extraction left checked out in the original
	// worktree. It is empty for ordinary creation.
	ReturnBranch string `json:"-"`
	// Container is the container the creator opened for the worktree.
	Container OpenedContainer `json:"-"`
}

// WorktreeCreator creates a worktree and, when appropriate, opens its
// container in the selected backend.
type WorktreeCreator interface {
	CreateWorktree(context.Context, WorktreeCreateOptions) (Worktree, error)
}

// WorktreeExtractOptions controls moving the branch checked out in the
// original worktree into a new worktree.
type WorktreeExtractOptions struct {
	Repo         string
	Destination  string
	ReturnBranch string
	// NoFocus asks the backend to open the extracted worktree container
	// without moving the user's view to it.
	NoFocus bool
}

// WorktreeExtractor moves the branch checked out in the original worktree
// into a new worktree and opens it.
type WorktreeExtractor interface {
	ExtractWorktree(context.Context, WorktreeExtractOptions) (Worktree, error)
}

// WorktreeRemover rolls back a worktree created by this invocation.
type WorktreeRemover interface {
	RemoveWorktree(context.Context, Worktree) error
}

// WorktreeExtractionRollback removes only resources created by an extraction
// and restores the extracted branch in the original checkout. It is separate
// from WorktreeRemover because ordinary creation has no branch to restore.
type WorktreeExtractionRollback interface {
	RollbackExtractWorktree(context.Context, Worktree) error
}

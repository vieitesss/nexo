package capabilities

import "context"

// ContainerActions are the operations the place picker can perform on one
// machine's containers. A resolver selects these actions by machine so adding
// remote machines does not change picker command routing.
type ContainerActions interface {
	ContainerOpener
	ContainerRenamer
	ContainerKiller
}

// ContainerActionResolver chooses container actions for a machine. The
// machine value is the stable namespace carried by a place, not a display
// label guessed by the command.
type ContainerActionResolver interface {
	ResolveContainerActions(context.Context, string) (ContainerActions, error)
}

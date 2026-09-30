package capabilities

import (
	"context"
	"time"
)

// Container is a top-level unit owned by a multiplexer. Machine identifies the
// computer that owns it; IDs are backend-native and stable for the lifetime of
// the container.
type Container struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Machine      string     `json:"machine"`
	ActivePaneID string     `json:"active_pane_id,omitempty"`
	Preview      string     `json:"preview,omitempty"`
	Activity     *time.Time `json:"activity,omitempty"`
}

// ContainerLister lists containers owned by the selected backend.
type ContainerLister interface {
	ListContainers(context.Context) ([]Container, error)
}

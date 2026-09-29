package capabilities

// Pane is a pane exposed by a backend.
type Pane struct {
	ID          string `json:"id"`
	ContainerID string `json:"container_id,omitempty"`
	Title       string `json:"title,omitempty"`
	TTY         string `json:"tty,omitempty"`
}

// Agent is an agent pane exposed by a backend.
type Agent struct {
	ID          string `json:"id"`
	PaneID      string `json:"pane_id"`
	Name        string `json:"name"`
	Working     bool   `json:"working"`
	SeenStopped bool   `json:"seen_stopped"`
}

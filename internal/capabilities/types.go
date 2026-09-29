package capabilities

import "time"

// Pane is a pane exposed by a backend.
type Pane struct {
	ID          string `json:"id"`
	ContainerID string `json:"container_id,omitempty"`
	Title       string `json:"title,omitempty"`
	TTY         string `json:"tty,omitempty"`
}

// Agent is an agent pane exposed by a backend.
type Agent struct {
	PaneID       string     `json:"pane_id"`
	Target       string     `json:"target"`
	Harness      string     `json:"harness"`
	State        string     `json:"state"`
	Seen         bool       `json:"seen"`
	StoppedSince *time.Time `json:"stopped_since,omitempty"`
	Branch       string     `json:"branch,omitempty"`
}

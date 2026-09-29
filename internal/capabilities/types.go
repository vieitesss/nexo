package capabilities

import "time"

// Pane is a pane exposed by a backend.
type Pane struct {
	ID          string `json:"id"`
	ContainerID string `json:"container_id,omitempty"`
	Title       string `json:"title,omitempty"`
	TTY         string `json:"tty,omitempty"`
}

// Agent is an agent pane exposed by a backend. ContainerID is the stable
// backend-native container identity; Container is only a display label and may
// be omitted or duplicated.
type Agent struct {
	PaneID       string     `json:"pane_id"`
	Target       string     `json:"target"`
	ContainerID  string     `json:"container_id,omitempty"`
	Container    string     `json:"container,omitempty"`
	Window       string     `json:"window,omitempty"`
	Harness      string     `json:"harness"`
	State        string     `json:"state"`
	Seen         bool       `json:"seen"`
	StoppedSince *time.Time `json:"stopped_since,omitempty"`
	Branch       string     `json:"branch,omitempty"`
}

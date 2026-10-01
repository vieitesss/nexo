package capabilities

import "time"

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
	Focused      bool       `json:"-"`
	StoppedSince *time.Time `json:"stopped_since,omitempty"`
	Branch       string     `json:"branch,omitempty"`
}

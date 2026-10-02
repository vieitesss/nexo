package capabilities

import "context"

// GlanceAction is one user-bound glance command.
type GlanceAction string

const (
	GlanceOn     GlanceAction = "on"
	GlanceOff    GlanceAction = "off"
	GlanceToggle GlanceAction = "toggle"
)

// GlanceController applies one glance action to a backend.
type GlanceController interface {
	Glance(context.Context, GlanceAction) error
}

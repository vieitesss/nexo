package herdr

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

// ProjectBehaviors reports splits and tabs. Herdr has no floating pane: its
// popups are session-modal and cannot open while nexo's own popup is active.
func (b *Backend) ProjectBehaviors() []config.ProjectBehavior {
	return []config.ProjectBehavior{config.BehaviorSplitRight, config.BehaviorSplitDown, config.BehaviorTab}
}

// PlaceProject opens a shell in path beside the origin pane. Without
// NEXO_ORIGIN_PANE, Herdr splits the calling pane, or the focused pane when
// called from a popup, which has no HERDR_PANE_ID.
func (b *Backend) PlaceProject(ctx context.Context, path string, behavior config.ProjectBehavior) error {
	if b == nil || b.Runner == nil {
		return errors.New("herdr project placement requires a command runner")
	}
	var args []string
	switch behavior {
	case config.BehaviorSplitRight, config.BehaviorSplitDown:
		args = []string{"split"}
		if origin := capabilities.OriginPane(); origin != "" {
			args = append(args, origin)
		}
		direction := "right"
		if behavior == config.BehaviorSplitDown {
			direction = "down"
		}
		args = append(args, "--direction", direction, "--cwd", path, "--focus")
		_, err := b.run(ctx, "pane", args...)
		return err
	case config.BehaviorTab:
		_, err := b.run(ctx, "tab", "create", "--cwd", path, "--focus")
		return err
	default:
		return fmt.Errorf("herdr does not support project behavior %q", behavior)
	}
}

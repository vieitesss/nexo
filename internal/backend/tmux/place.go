package tmux

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

// ProjectBehaviors reports splits and windows. tmux popups close with their
// command and block all other input, so they are not offered as a float.
func (b *Backend) ProjectBehaviors() []config.ProjectBehavior {
	return []config.ProjectBehavior{config.BehaviorSplitRight, config.BehaviorSplitDown, config.BehaviorTab}
}

// PlaceProject opens a shell in path beside the origin pane. Without
// NEXO_ORIGIN_PANE, tmux targets the client's current pane, which is the pane
// under a display-popup.
func (b *Backend) PlaceProject(ctx context.Context, path string, behavior config.ProjectBehavior) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux project placement requires a command runner")
	}
	var operation string
	var args []string
	switch behavior {
	case config.BehaviorSplitRight:
		operation, args = "split-window", []string{"-h"}
	case config.BehaviorSplitDown:
		operation, args = "split-window", []string{"-v"}
	case config.BehaviorTab:
		operation = "new-window"
	default:
		return fmt.Errorf("tmux does not support project behavior %q", behavior)
	}
	args = append(args, "-c", path)
	if origin := capabilities.OriginPane(); origin != "" {
		args = append(args, "-t", origin)
	}
	_, err := b.run(ctx, operation, args...)
	return err
}

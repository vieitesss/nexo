package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

// floatScript opens a shell in a floating layer. The CLI has no float
// command, so it runs through `rex do` with the path passed as JSON args.
const floatScript = `local a = rex.args
return rex.session.new_layer({
	session_id = a.session_id,
	bounds = { 0.1, 0.1, 0.8, 0.8 },
	focus = true,
	layout = rex.layout.block({
		flavor = "com.superlogical.terminal.shell",
		options = { cwd = a.cwd },
	}),
})`

// ProjectBehaviors reports every behavior: Rex has splits, windows, and
// floating layers.
func (b *Backend) ProjectBehaviors() []config.ProjectBehavior {
	return []config.ProjectBehavior{config.BehaviorSplitRight, config.BehaviorSplitDown, config.BehaviorTab, config.BehaviorFloat}
}

// PlaceProject opens a shell in path. Splits anchor on NEXO_ORIGIN_PANE when
// set: nexo's own floating block is focused while it runs, so a launcher
// that opens nexo in a float must name the block the user came from.
func (b *Backend) PlaceProject(ctx context.Context, path string, behavior config.ProjectBehavior) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex project placement requires a command runner")
	}
	switch behavior {
	case config.BehaviorSplitRight, config.BehaviorSplitDown:
		direction := "right"
		if behavior == config.BehaviorSplitDown {
			direction = "below"
		}
		args := []string{"split", "--split", direction, "-c", path}
		if origin := capabilities.OriginPane(); origin != "" {
			args = append(args, "-b", origin)
		}
		_, err := b.exec(ctx, "split", args...)
		return err
	case config.BehaviorTab:
		_, err := b.exec(ctx, "window new", "window", "new", "-c", path)
		return err
	case config.BehaviorFloat:
		input := map[string]string{"cwd": path}
		if session := strings.TrimSpace(os.Getenv("REX_SESSION")); session != "" {
			input["session_id"] = session
		}
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode rex float input: %w", err)
		}
		_, err = b.exec(ctx, "float", "do", "-e", floatScript, "--args", string(payload))
		return err
	default:
		return fmt.Errorf("rex does not support project behavior %q", behavior)
	}
}

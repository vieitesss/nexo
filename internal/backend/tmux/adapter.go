package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
)

var _ capabilities.TmuxInitializer = (*Backend)(nil)

// Init installs hooks and starts the per-server agent watcher. Key bindings
// belong to the user's tmux.conf.
func (b *Backend) Init(ctx context.Context, input capabilities.InitInput) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux adapter requires a command runner")
	}
	b.agents.ConfigureHookCommand(os.Args[0], input.ConfigPath)
	if err := b.agents.ReplaceWatcher(ctx); err != nil {
		return err
	}
	if err := b.agents.installHooks(ctx); err != nil {
		return err
	}
	if _, err := b.run(ctx, "run-shell", "-b", watcherCommand(input.ConfigPath)); err != nil {
		return fmt.Errorf("start tmux agent watcher: %w", err)
	}
	return nil
}

func watcherCommand(configPath string) string {
	command := fmt.Sprintf("%s --backend=%s", shellQuote(os.Args[0]), shellQuote("tmux"))
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	return command + " agents watch"
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

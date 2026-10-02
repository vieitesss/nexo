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

// watcherCommand builds the shell command that starts the agent watcher. The
// binary is its absolute unresolved launch path when that can be determined,
// so the watcher does not need a PATH lookup to find its own executable when
// it later derives the path to watch.
func watcherCommand(configPath string) string {
	binary := os.Args[0]
	if path, err := launchPath(); err == nil && path != "" {
		binary = path
	}
	command := fmt.Sprintf("%s --backend=%s", shellQuote(binary), shellQuote("tmux"))
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

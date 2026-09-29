package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

const (
	defaultPlaceKey = "C-s"
	defaultAgentKey = "a"

	placePickerCommand = "nexo --backend=tmux"
	agentPickerCommand = "nexo --backend=tmux agents pick"
	agentWatchCommand  = "nexo --backend=tmux agents watch"
)

// Init installs nexo's tmux bindings and starts the per-server agent watcher.
// The adapter remains a thin command surface: configuration and command
// construction are resolved here by the nexo binary rather than by the TPM
// entry file.
func (b *Backend) Init(ctx context.Context, cfg config.TmuxConfig) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux adapter requires a command runner")
	}
	cfg = cfg.WithDefaults()

	if err := b.bind(ctx, "-n", cfg.PlaceKey, defaultPlaceKey, placePickerCommand, cfg); err != nil {
		return err
	}
	if err := b.bind(ctx, "", cfg.AgentKey, defaultAgentKey, agentPickerCommand, cfg); err != nil {
		return err
	}
	if err := b.agents.installHooks(ctx); err != nil {
		return err
	}
	if _, err := b.run(ctx, "run-shell", "-b", agentWatchCommand); err != nil {
		return fmt.Errorf("start tmux agent watcher: %w", err)
	}
	return nil
}

func (b *Backend) bind(ctx context.Context, mode, key, unbindKey, command string, cfg config.TmuxConfig) error {
	unbindArgs := []string{}
	if mode != "" {
		unbindArgs = append(unbindArgs, mode)
	}
	if strings.EqualFold(strings.TrimSpace(key), "none") {
		key = ""
	}
	if key == "" {
		unbindArgs = append(unbindArgs, unbindKey)
	} else {
		unbindArgs = append(unbindArgs, key)
	}
	if _, err := b.run(ctx, "unbind-key", unbindArgs...); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	bindArgs := []string{}
	if mode != "" {
		bindArgs = append(bindArgs, mode)
	}
	bindArgs = append(bindArgs, key, "display-popup", "-E", "-w", cfg.PopupWidth, "-h", cfg.PopupHeight, "-x", cfg.PopupX, "-y", cfg.PopupY, command)
	if _, err := b.run(ctx, "bind-key", bindArgs...); err != nil {
		return err
	}
	return nil
}

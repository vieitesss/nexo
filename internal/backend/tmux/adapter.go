package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

const (
	placeBindingOption = "@nexo-place-key"
	agentBindingOption = "@nexo-agent-key"
)

// InitInput contains the tmux adapter settings for one invocation.
type InitInput struct {
	Config     config.TmuxConfig
	ConfigPath string
}

// Init installs nexo's tmux bindings and starts the per-server agent watcher.
// The adapter remains a thin command surface: configuration and command
// construction are resolved here by the nexo binary rather than by the TPM
// entry file.
func (b *Backend) Init(ctx context.Context, input InitInput) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux adapter requires a command runner")
	}
	cfg := input.Config.WithDefaults()

	placeCommand := pickerCommand(input.ConfigPath)
	if err := b.bind(ctx, "-n", "root", placeBindingOption, cfg.PlaceKey, "tmux.place-key", placeCommand, cfg); err != nil {
		return err
	}
	agentCommand := agentPickerCommand(input.ConfigPath)
	if err := b.bind(ctx, "", "prefix", agentBindingOption, cfg.AgentKey, "tmux.agent-key", agentCommand, cfg); err != nil {
		return err
	}
	if err := b.agents.installHooks(ctx); err != nil {
		return err
	}
	if _, err := b.run(ctx, "run-shell", "-b", "nexo --backend=tmux agents watch"); err != nil {
		return fmt.Errorf("start tmux agent watcher: %w", err)
	}
	return nil
}

func (b *Backend) bind(ctx context.Context, mode, table, option, key, configOption, command string, cfg config.TmuxConfig) error {
	key = normalizeKey(key)
	previousTable, previousKey, err := b.recordedBinding(ctx, option, table)
	if err != nil {
		return err
	}

	// The option is only a hint about where nexo last installed a binding. A
	// user can replace that binding without knowing about the option, so always
	// inspect the current command before treating it as ours.
	var previousBinding string
	var previousBound bool
	if previousKey != "" {
		previousBinding, previousBound, err = b.currentBinding(ctx, previousTable, previousKey)
		if err != nil {
			return err
		}
	}
	previousIsNexo := previousBound && isNexoBinding(previousBinding)
	previousIsTarget := previousKey != "" && previousTable == table && previousKey == key

	var targetBinding string
	var targetBound bool
	if key != "" {
		if previousIsTarget {
			targetBinding, targetBound = previousBinding, previousBound
		} else {
			targetBinding, targetBound, err = b.currentBinding(ctx, table, key)
			if err != nil {
				return err
			}
		}
	}

	if previousBound && !previousIsNexo && !previousIsTarget {
		if err := b.reportConflict(ctx, previousTable, previousKey, configOption); err != nil {
			return err
		}
	}
	if previousBound && previousIsNexo && !previousIsTarget {
		if err := b.unbind(ctx, previousTable, previousKey); err != nil {
			return err
		}
	}
	if targetBound && !isNexoBinding(targetBinding) {
		if err := b.reportConflict(ctx, table, key, configOption); err != nil {
			return err
		}
		return nil
	}
	if key == "" {
		return b.recordBinding(ctx, option, table, key)
	}

	bindArgs := []string{}
	if mode != "" {
		bindArgs = append(bindArgs, mode)
	}
	bindArgs = append(bindArgs, key, "display-popup", "-E", "-w", cfg.PopupWidth, "-h", cfg.PopupHeight, "-x", cfg.PopupX, "-y", cfg.PopupY, command)
	if _, err := b.run(ctx, "bind-key", bindArgs...); err != nil {
		return err
	}
	return b.recordBinding(ctx, option, table, key)
}

func (b *Backend) currentBinding(ctx context.Context, table, key string) (string, bool, error) {
	binding, err := b.run(ctx, "list-keys", "-T", table, key)
	if err != nil {
		// tmux exits non-zero when a key is not bound. Other failures (for
		// example, a missing server) must not be mistaken for ownership.
		if isMissingKey(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read tmux binding for %s:%s: %w", table, key, err)
	}
	return string(binding), strings.TrimSpace(string(binding)) != "", nil
}

func isMissingKey(err error) bool {
	var tmuxErr *Error
	if !errors.As(err, &tmuxErr) {
		return false
	}
	message := strings.ToLower(tmuxErr.Stderr)
	return strings.Contains(message, "unknown key") ||
		strings.Contains(message, "no such key") ||
		strings.Contains(message, "key not found")
}

func isNexoBinding(binding string) bool {
	return strings.Contains(binding, "NEXO_PLACE_PICKER_INLINE=1") ||
		strings.Contains(binding, "NEXO_AGENT_NAVIGATOR_INLINE=1") ||
		strings.Contains(binding, "nexo --backend=tmux") ||
		strings.Contains(binding, "nexo --backend tmux")
}

func (b *Backend) reportConflict(ctx context.Context, table, key, configOption string) error {
	message := fmt.Sprintf("nexo: tmux key %q in table %q is already bound; change %s or set it to 'none'", key, table, configOption)
	if _, err := b.run(ctx, "display-message", message); err != nil {
		return fmt.Errorf("report tmux key conflict: %w", err)
	}
	return nil
}

func normalizeKey(key string) string {
	if strings.EqualFold(strings.TrimSpace(key), "none") {
		return ""
	}
	return strings.TrimSpace(key)
}

func (b *Backend) recordedBinding(ctx context.Context, option, defaultTable string) (string, string, error) {
	output, err := b.run(ctx, "show-option", "-gqv", option)
	if err != nil {
		return "", "", fmt.Errorf("read tmux option %s: %w", option, err)
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return defaultTable, "", nil
	}
	table, key, ok := strings.Cut(value, ":")
	if !ok {
		return defaultTable, normalizeKey(value), nil
	}
	return table, normalizeKey(key), nil
}

func (b *Backend) recordBinding(ctx context.Context, option, table, key string) error {
	value := table + ":" + key
	if _, err := b.run(ctx, "set-option", "-g", option, value); err != nil {
		return fmt.Errorf("record tmux binding: %w", err)
	}
	return nil
}

func (b *Backend) unbind(ctx context.Context, table, key string) error {
	args := []string{}
	if table == "root" {
		args = append(args, "-n")
	}
	args = append(args, key)
	if _, err := b.run(ctx, "unbind-key", args...); err != nil {
		return fmt.Errorf("remove previous tmux binding: %w", err)
	}
	return nil
}

func pickerCommand(configPath string) string {
	command := fmt.Sprintf("NEXO_PLACE_PICKER_INLINE=1 %s --backend=%s", shellQuote(os.Args[0]), shellQuote("tmux"))
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	return command
}

func agentPickerCommand(configPath string) string {
	command := fmt.Sprintf("NEXO_AGENT_NAVIGATOR_INLINE=1 %s --backend=%s", shellQuote(os.Args[0]), shellQuote("tmux"))
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	return command + " agents pick"
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

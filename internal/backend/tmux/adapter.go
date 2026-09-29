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
	b.agents.ConfigureHookCommand(os.Args[0], input.ConfigPath)
	if err := b.agents.installHooks(ctx); err != nil {
		return err
	}
	if _, err := b.run(ctx, "run-shell", "-b", watcherCommand(input.ConfigPath)); err != nil {
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
	bindArgs = append(bindArgs, tmuxKey(key), "display-popup", "-E", "-w", cfg.PopupWidth, "-h", cfg.PopupHeight, "-x", cfg.PopupX, "-y", cfg.PopupY, command)
	if _, err := b.run(ctx, "bind-key", bindArgs...); err != nil {
		return err
	}
	return b.recordBinding(ctx, option, table, key)
}

func (b *Backend) currentBinding(ctx context.Context, table, key string) (string, bool, error) {
	binding, err := b.run(ctx, "list-keys", "-T", table, tmuxKey(key))
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
	words := tmuxWords(binding)
	for index, word := range words {
		if word != "display-popup" || index+1 >= len(words) {
			continue
		}
		command := tmuxWords(words[len(words)-1])
		if len(command) < 3 || command[2] != "--backend=tmux" {
			continue
		}
		if command[0] == "NEXO_PLACE_PICKER_INLINE=1" && validConfigArgs(command[3:]) {
			return true
		}
		if command[0] == "NEXO_AGENT_NAVIGATOR_INLINE=1" && len(command) >= 5 && command[len(command)-2] == "agents" && command[len(command)-1] == "pick" && validConfigArgs(command[3:len(command)-2]) {
			return true
		}
	}
	return false
}

// tmuxWords tokenizes the command serialization returned by list-keys. It is
// deliberately small: tmux's quoting is sufficient here because we only need
// to distinguish the final display-popup command from arbitrary user text.
func validConfigArgs(args []string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--config-file=") {
			return false
		}
	}
	return true
}

func tmuxWords(value string) []string {
	var words []string
	var word strings.Builder
	var quote byte
	escaped := false
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for i := 0; i < len(value); i++ {
		char := value[i]
		if escaped {
			word.WriteByte(char)
			escaped = false
			continue
		}
		if quote == '"' && char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteByte(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			word.WriteByte(char)
		}
	}
	if escaped {
		word.WriteByte('\\')
	}
	flush()
	return words
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
	if table != "root" && table != "prefix" {
		return "", "", fmt.Errorf("unsupported recorded tmux key table %q", table)
	}
	return table, normalizeKey(key), nil
}

func (b *Backend) recordBinding(ctx context.Context, option, table, key string) error {
	value := table + ":" + key
	if _, err := b.run(ctx, "set-option", "-g", option, tmuxKey(value)); err != nil {
		return fmt.Errorf("record tmux binding: %w", err)
	}
	return nil
}

func (b *Backend) unbind(ctx context.Context, table, key string) error {
	if table != "root" && table != "prefix" {
		return fmt.Errorf("unsupported tmux key table %q", table)
	}
	if _, err := b.run(ctx, "unbind-key", "-T", table, tmuxKey(key)); err != nil {
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

func watcherCommand(configPath string) string {
	command := fmt.Sprintf("%s --backend=%s", shellQuote(os.Args[0]), shellQuote("tmux"))
	if configPath != "" {
		command += " --config-file=" + shellQuote(configPath)
	}
	return command + " agents watch"
}

func tmuxKey(key string) string {
	return strings.ReplaceAll(key, ";", `\;`)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

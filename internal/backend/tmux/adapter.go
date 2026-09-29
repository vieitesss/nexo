package tmux

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

const (
	placeBindingOption = "@nexo-place-key"
	agentBindingOption = "@nexo-agent-key"

	bindingRecordVersion = "v1"
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
	previous, hasPrevious, err := b.recordedBinding(ctx, option)
	if err != nil {
		return err
	}
	if hasPrevious && !previous.verified {
		if err := b.reportUnverifiedBinding(ctx, previous.key); err != nil {
			return err
		}
		hasPrevious = false
	}

	var previousBinding string
	var previousBound bool
	if hasPrevious {
		previousBinding, previousBound, err = b.currentBinding(ctx, previous.table, previous.key)
		if err != nil {
			return err
		}
	}
	previousIsTarget := hasPrevious && previous.table == table && previous.key == key
	previousOwned := previousBound && previousBinding == previous.line
	conflictReported := false
	if previousBound && !previousOwned {
		if err := b.reportConflict(ctx, previous.table, previous.key, configOption); err != nil {
			return err
		}
		conflictReported = true
	}

	var targetBound bool
	if key != "" {
		if previousIsTarget {
			targetBound = previousBound
		} else {
			_, targetBound, err = b.currentBinding(ctx, table, key)
			if err != nil {
				return err
			}
		}
	}
	targetOwned := previousIsTarget && previousOwned && targetBound
	if targetBound && !targetOwned {
		if !conflictReported || !previousIsTarget {
			if err := b.reportConflict(ctx, table, key, configOption); err != nil {
				return err
			}
		}
		return nil
	}

	if key == "" {
		if previousOwned {
			if err := b.unbind(ctx, previous.table, previous.key); err != nil {
				return err
			}
		}
		return b.recordBinding(ctx, option, table, key, "")
	}
	if hasPrevious && previousOwned && !previousIsTarget {
		if err := b.unbind(ctx, previous.table, previous.key); err != nil {
			return err
		}
	}

	bindArgs := []string{}
	if mode != "" {
		bindArgs = append(bindArgs, mode)
	}
	bindArgs = append(bindArgs, tmuxKey(key), "display-popup", "-E", "-w", cfg.PopupWidth, "-h", cfg.PopupHeight, "-x", cfg.PopupX, "-y", cfg.PopupY, command)
	if _, err := b.run(ctx, "bind-key", bindArgs...); err != nil {
		return err
	}
	installed, bound, err := b.currentBinding(ctx, table, key)
	if err != nil {
		return fmt.Errorf("read installed tmux binding for %s:%s: %w", table, key, err)
	}
	if !bound {
		return fmt.Errorf("read installed tmux binding for %s:%s: binding is missing", table, key)
	}
	return b.recordBinding(ctx, option, table, key, installed)
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

func (b *Backend) reportConflict(ctx context.Context, table, key, configOption string) error {
	message := fmt.Sprintf("nexo: tmux key %q in table %q is already bound; change %s or set it to 'none'", key, table, configOption)
	if _, err := b.run(ctx, "display-message", message); err != nil {
		return fmt.Errorf("report tmux key conflict: %w", err)
	}
	return nil
}

func (b *Backend) reportUnverifiedBinding(ctx context.Context, key string) error {
	message := fmt.Sprintf("nexo: couldn't verify its previous binding for key %q; left it alone", key)
	if _, err := b.run(ctx, "display-message", message); err != nil {
		return fmt.Errorf("report unverified tmux binding: %w", err)
	}
	return nil
}

func normalizeKey(key string) string {
	if strings.EqualFold(strings.TrimSpace(key), "none") {
		return ""
	}
	return strings.TrimSpace(key)
}

type bindingRecord struct {
	table    string
	key      string
	line     string
	verified bool
}

func (b *Backend) recordedBinding(ctx context.Context, option string) (bindingRecord, bool, error) {
	output, err := b.run(ctx, "show-option", "-gqv", option)
	if err != nil {
		return bindingRecord{}, false, fmt.Errorf("read tmux option %s: %w", option, err)
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return bindingRecord{}, false, nil
	}
	record, ok, err := decodeBindingRecord(value)
	if err != nil {
		// A malformed v1 record still identifies an unsupported table when its
		// table field is intact. Keep rejecting that case rather than allowing
		// an unknown table to influence a later mutation.
		parts := strings.Split(value, ":")
		if len(parts) == 4 && parts[0] == bindingRecordVersion && parts[1] != "root" && parts[1] != "prefix" {
			return bindingRecord{}, false, err
		}
		return bindingRecord{key: recordedBindingKey(value)}, true, nil
	}
	if ok {
		record.verified = true
		return record, true, nil
	}
	// Older development versions recorded only table:key. They cannot
	// establish ownership, so leave any binding untouched and let bind apply
	// normal first-run rules to the configured key.
	if table, _, found := strings.Cut(value, ":"); found && table != "root" && table != "prefix" {
		return bindingRecord{}, false, fmt.Errorf("unsupported recorded tmux key table %q", table)
	}
	return bindingRecord{key: recordedBindingKey(value)}, true, nil
}

func recordedBindingKey(value string) string {
	parts := strings.Split(value, ":")
	if len(parts) == 4 && parts[0] == bindingRecordVersion && (parts[1] == "root" || parts[1] == "prefix") {
		if key, err := base64.RawStdEncoding.DecodeString(parts[2]); err == nil && len(key) > 0 {
			return string(key)
		}
	}
	if _, key, found := strings.Cut(value, ":"); found && key != "" {
		return key
	}
	return value
}

func decodeBindingRecord(value string) (bindingRecord, bool, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 || parts[0] != bindingRecordVersion {
		return bindingRecord{}, false, nil
	}
	if parts[1] != "root" && parts[1] != "prefix" {
		return bindingRecord{}, false, fmt.Errorf("unsupported recorded tmux key table %q", parts[1])
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return bindingRecord{}, false, fmt.Errorf("decode recorded tmux key: %w", err)
	}
	line, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return bindingRecord{}, false, fmt.Errorf("decode recorded tmux binding: %w", err)
	}
	if len(key) == 0 || len(line) == 0 {
		return bindingRecord{}, false, fmt.Errorf("recorded tmux binding is incomplete")
	}
	return bindingRecord{table: parts[1], key: string(key), line: string(line), verified: true}, true, nil
}

func encodeBindingRecord(table, key, line string) string {
	return strings.Join([]string{
		bindingRecordVersion,
		table,
		base64.RawStdEncoding.EncodeToString([]byte(key)),
		base64.RawStdEncoding.EncodeToString([]byte(line)),
	}, ":")
}

func (b *Backend) recordBinding(ctx context.Context, option, table, key, line string) error {
	value := ""
	if key != "" {
		value = encodeBindingRecord(table, key, line)
	}
	if _, err := b.run(ctx, "set-option", "-g", option, value); err != nil {
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

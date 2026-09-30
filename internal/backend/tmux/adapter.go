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

// Init removes bindings from older versions when nexo can prove ownership,
// then installs hooks and starts the per-server agent watcher. Key bindings
// belong to the user's tmux.conf now.
func (b *Backend) Init(ctx context.Context, input InitInput) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux adapter requires a command runner")
	}
	for _, option := range []string{placeBindingOption, agentBindingOption} {
		if err := b.removeRecordedBinding(ctx, option); err != nil {
			return err
		}
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

func (b *Backend) removeRecordedBinding(ctx context.Context, option string) error {
	record, found, err := b.recordedBinding(ctx, option)
	if err != nil {
		return err
	}
	if found && record.verified {
		current, bound, err := b.currentBinding(ctx, record.table, record.key)
		if err != nil {
			return err
		}
		if bound && current == record.line {
			if err := b.unbind(ctx, record.table, record.key); err != nil {
				return err
			}
		}
	}
	return b.recordBinding(ctx, option, "", "", "")
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
	if err != nil || !ok {
		// Only a complete ownership record can authorize removing a binding.
		return bindingRecord{}, false, nil
	}
	record.verified = true
	return record, true, nil
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

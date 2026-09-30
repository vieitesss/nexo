package tmux

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
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
	Stderr     io.Writer
}

// Init removes bindings from older versions when nexo can prove ownership,
// then installs hooks and starts the per-server agent watcher. Key bindings
// belong to the user's tmux.conf now.
func (b *Backend) Init(ctx context.Context, input InitInput) error {
	if b == nil || b.Runner == nil {
		return errors.New("tmux adapter requires a command runner")
	}
	stderr := input.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	for _, option := range []string{placeBindingOption, agentBindingOption} {
		if err := b.removeRecordedBinding(ctx, option, stderr); err != nil {
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

func (b *Backend) removeRecordedBinding(ctx context.Context, option string, stderr io.Writer) error {
	record, found, err := b.recordedBinding(ctx, option)
	if err != nil {
		return err
	}
	if found && !record.verified {
		if stderr != nil {
			location := "ownership record " + option
			if record.table != "" || record.key != "" {
				location = fmt.Sprintf("tmux key %s %s", record.table, record.key)
			}
			_, warningErr := fmt.Fprintf(stderr, "nexo no longer binds keys; check %s, nexo could not verify it\n", location)
			unsetErr := b.unsetRecord(ctx, option)
			if warningErr != nil {
				if unsetErr != nil {
					return errors.Join(warningErr, unsetErr)
				}
				return warningErr
			}
			return unsetErr
		}
		return b.unsetRecord(ctx, option)
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
	return b.unsetRecord(ctx, option)
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
		// The caller warns before discarding this unverifiable record.
		return parseUnverifiableRecord(value), true, nil
	}
	record.verified = true
	return record, true, nil
}

func parseUnverifiableRecord(value string) bindingRecord {
	parts := strings.Split(value, ":")
	if len(parts) >= 2 {
		table, key := parts[0], parts[1]
		if parts[0] == bindingRecordVersion && len(parts) >= 3 {
			table, key = parts[1], parts[2]
			if decoded, err := base64.RawStdEncoding.DecodeString(parts[2]); err == nil && len(decoded) > 0 {
				key = string(decoded)
			}
		}
		return bindingRecord{table: table, key: key}
	}
	return bindingRecord{}
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

func (b *Backend) unsetRecord(ctx context.Context, option string) error {
	if _, err := b.run(ctx, "set-option", "-gu", option); err != nil {
		return fmt.Errorf("delete tmux ownership record: %w", err)
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

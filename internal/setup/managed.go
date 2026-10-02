// Package setup implements the guided first-run and repair workflow.
package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

const (
	ManagedStart = "# >>> nexo >>>"
	ManagedEnd   = "# <<< nexo <<<"
)

// ReplaceManagedBlock replaces the one block owned by nexo. It removes older
// duplicate blocks as well, making a setup run repair hand-edited duplicates.
func ReplaceManagedBlock(contents, body string) (string, bool, error) {
	lines := strings.SplitAfter(contents, "\n")
	out := make([]string, 0, len(lines)+3)
	inside := false
	for _, line := range lines {
		trim := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if trim == ManagedStart {
			if inside {
				return "", false, errors.New("nested nexo managed block")
			}
			inside = true
			continue
		}
		if trim == ManagedEnd {
			if !inside {
				return "", false, errors.New("nexo managed block has an end without a start")
			}
			inside = false
			continue
		}
		if !inside {
			out = append(out, line)
		}
	}
	if inside {
		return "", false, errors.New("nexo managed block is missing its end")
	}
	cleaned := strings.Join(out, "")
	cleaned = strings.TrimRight(cleaned, "\n")
	body = strings.Trim(body, "\n")
	if body != "" {
		if cleaned != "" {
			cleaned += "\n"
		}
		cleaned += ManagedStart + "\n" + body + "\n" + ManagedEnd + "\n"
	} else if cleaned != "" {
		cleaned += "\n"
	}
	return cleaned, cleaned != contents, nil
}

// Change is a reviewable file change. Before and After are intentionally kept
// in memory so callers can print a diff before any write occurs.
type Change struct {
	Machine string
	Path    string
	Before  []byte
	After   []byte
}

func (c Change) Changed() bool { return !bytes.Equal(c.Before, c.After) }

// ReadChange reads path and applies one managed block without writing it.
func ReadChange(machine, path, body string) (Change, error) {
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, fmt.Errorf("read %s: %w", path, err)
	}
	after, _, err := ReplaceManagedBlock(string(before), body)
	if err != nil {
		return Change{}, fmt.Errorf("prepare %s: %w", path, err)
	}
	return Change{Machine: machine, Path: path, Before: before, After: []byte(after)}, nil
}

// ApplyChange backs up a user's file before its first edit and writes it
// atomically. Backups use .nexo.bak and are never overwritten.
func ApplyChange(change Change) error {
	if !change.Changed() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(change.Path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", change.Path, err)
	}
	if len(change.Before) > 0 {
		backup := change.Path + ".nexo.bak"
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(backup, change.Before, 0o600); err != nil {
				return fmt.Errorf("backup %s: %w", change.Path, err)
			}
		}
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(change.Path); err == nil {
		mode = info.Mode().Perm()
	}
	temporary, err := os.CreateTemp(filepath.Dir(change.Path), ".nexo-setup-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = temporary.Close(); _ = os.Remove(temporaryPath) }
	if err := temporary.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := temporary.Write(change.After); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, change.Path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

// ShellQuote is POSIX quoting used by remote setup commands.
func ShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func run(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, name string, args ...string) ([]byte, error) {
	if machine == "" {
		return commandRunner.Run(ctx, name, args...)
	}
	sshArgs := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	if sshConfig != "" {
		sshArgs = append(sshArgs, "-F", sshConfig)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, ShellQuote(name))
	for _, arg := range args {
		parts = append(parts, ShellQuote(arg))
	}
	sshArgs = append(sshArgs, machine, strings.Join(parts, " "))
	return commandRunner.Run(ctx, "ssh", sshArgs...)
}

func runInput(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig string, input []byte, name string, args ...string) ([]byte, error) {
	inputRunner, ok := commandRunner.(runner.InputRunner)
	if !ok {
		return nil, errors.New("setup requires an input-capable command runner")
	}
	if machine == "" {
		return inputRunner.RunInput(ctx, input, name, args...)
	}
	sshArgs := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	if sshConfig != "" {
		sshArgs = append(sshArgs, "-F", sshConfig)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, ShellQuote(name))
	for _, arg := range args {
		parts = append(parts, ShellQuote(arg))
	}
	sshArgs = append(sshArgs, machine, strings.Join(parts, " "))
	return inputRunner.RunInput(ctx, input, "ssh", sshArgs...)
}

func writeLine(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}

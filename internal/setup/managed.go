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

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/remote"
	"github.com/vieitesss/nexo/internal/runner"
)

const (
	ManagedStart = "# >>> nexo >>>"
	ManagedEnd   = "# <<< nexo <<<"

	// LuaManagedStart and LuaManagedEnd delimit the same block in Lua files
	// such as Rex's init.lua, where # is not a comment leader.
	LuaManagedStart = "-- >>> nexo >>>"
	LuaManagedEnd   = "-- <<< nexo <<<"
)

// ReplaceManagedBlock replaces the first nexo block in place and removes later
// duplicates. Bytes outside marker lines are not normalized or moved.
func ReplaceManagedBlock(contents, body string) (string, bool, error) {
	return ReplaceManagedBlockBetween(contents, body, ManagedStart, ManagedEnd)
}

// ReplaceManagedBlockBetween is ReplaceManagedBlock with explicit comment
// markers. Rex hands back the same in-place, duplicate-repairing behavior in
// Lua comment syntax.
func ReplaceManagedBlockBetween(contents, body, startMarker, endMarker string) (string, bool, error) {
	body = strings.Trim(body, "\r\n")
	type block struct{ start, end int }
	blocks := []block{}
	inside := false
	start := -1
	offset := 0
	for _, line := range strings.SplitAfter(contents, "\n") {
		text := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if text == startMarker {
			if inside {
				return "", false, errors.New("nested nexo managed block")
			}
			inside = true
			start = offset
		}
		if text == endMarker {
			if !inside {
				return "", false, errors.New("nexo managed block has an end without a start")
			}
			end := offset + len(line)
			blocks = append(blocks, block{start: start, end: end})
			inside = false
		}
		offset += len(line)
	}
	if inside {
		return "", false, errors.New("nexo managed block is missing its end")
	}
	if len(blocks) == 0 {
		if body == "" {
			return contents, false, nil
		}
		separator := ""
		if len(contents) > 0 && contents[len(contents)-1] != '\n' {
			separator = "\n"
		}
		return contents + separator + startMarker + "\n" + body + "\n" + endMarker + "\n", true, nil
	}
	var out strings.Builder
	cursor := 0
	for index, current := range blocks {
		out.WriteString(contents[cursor:current.start])
		if index == 0 && body != "" {
			out.WriteString(startMarker)
			out.WriteByte('\n')
			out.WriteString(body)
			out.WriteByte('\n')
			out.WriteString(endMarker)
			// Preserve the newline convention of the original end marker.
			if current.end > current.start && contents[current.end-1] == '\n' {
				out.WriteByte('\n')
			}
		}
		cursor = current.end
	}
	out.WriteString(contents[cursor:])
	result := out.String()
	return result, result != contents, nil
}

// Change is a reviewable local or remote file change. Before and After remain
// in memory until one confirmation has accepted the complete plan.
type Change struct {
	Machine string
	Path    string
	Before  []byte
	After   []byte
	Mode    os.FileMode
	Existed bool
	Remote  bool
}

func (c Change) Changed() bool { return !bytes.Equal(c.Before, c.After) }

// ReadChange reads a local file and applies one managed block without writing.
func ReadChange(machine, path, body string) (Change, error) {
	return ReadChangeBetween(machine, path, body, ManagedStart, ManagedEnd)
}

// ReadChangeBetween is ReadChange for a file whose block uses custom markers.
func ReadChangeBetween(machine, path, body, startMarker, endMarker string) (Change, error) {
	before, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, fmt.Errorf("read %s: %w", path, err)
	}
	after, _, err := ReplaceManagedBlockBetween(string(before), body, startMarker, endMarker)
	if err != nil {
		return Change{}, fmt.Errorf("prepare %s: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Change{}, fmt.Errorf("stat %s: %w", path, statErr)
	}
	return Change{Machine: machine, Path: path, Before: before, After: []byte(after), Mode: mode, Existed: existed}, nil
}

// ApplyChange backs up a user's file before its first edit and applies planned
// bytes atomically. All callers must have displayed the Change first.
func ApplyChange(change Change) error {
	if !change.Changed() {
		return nil
	}
	if change.Remote {
		return errors.New("remote changes must be applied through the remote plan")
	}
	if err := os.MkdirAll(filepath.Dir(change.Path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", change.Path, err)
	}
	if change.Existed {
		backup := change.Path + ".nexo.bak"
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(backup, change.Before, 0o600); err != nil {
				return fmt.Errorf("backup %s: %w", change.Path, err)
			}
		} else if err != nil {
			return fmt.Errorf("check backup %s: %w", backup, err)
		}
	}
	if change.Mode == 0 {
		change.Mode = 0o600
	}
	return config.WritePrepared(change.Path, change.After, change.Mode)
}

// ShellQuote is POSIX quoting used by remote setup commands.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// run uses the same SSH argument and shell-command construction as the remote
// module. This keeps setup's probes compatible with remote list/focus.
func run(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, name string, args ...string) ([]byte, error) {
	if machine == "" {
		return commandRunner.Run(ctx, name, args...)
	}
	return commandRunner.Run(ctx, "ssh", remote.SSHCommandArgs(sshConfig, machine, remote.ShellCommand(append([]string{name}, args...)...))...)
}

func runInput(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig string, input []byte, name string, args ...string) ([]byte, error) {
	inputRunner, ok := commandRunner.(runner.InputRunner)
	if !ok {
		return nil, errors.New("setup requires an input-capable command runner")
	}
	if machine == "" {
		return inputRunner.RunInput(ctx, input, name, args...)
	}
	return inputRunner.RunInput(ctx, input, "ssh", remote.SSHCommandArgs(sshConfig, machine, remote.ShellCommand(append([]string{name}, args...)...))...)
}

// readRemoteFile distinguishes a missing file from a failed read. A failed
// read is never treated as an empty file.
func readRemoteFile(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, path string) ([]byte, bool, error) {
	const markerPresent = "__NEXO_FILE_PRESENT__"
	const markerMissing = "__NEXO_FILE_MISSING__"
	script := "if test -e " + remote.ShellCommand(path) + "; then printf '" + markerPresent + "\\n'; cat " + remote.ShellCommand(path) + "; else printf '" + markerMissing + "\\n'; fi"
	out, err := run(ctx, commandRunner, machine, sshConfig, "sh", "-c", script)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	line, rest, ok := strings.Cut(string(out), "\n")
	if !ok {
		return nil, false, fmt.Errorf("read %s: invalid file probe response", path)
	}
	switch strings.TrimSpace(line) {
	case markerMissing:
		return nil, false, nil
	case markerPresent:
		return []byte(rest), true, nil
	default:
		return nil, false, fmt.Errorf("read %s: invalid file probe response", path)
	}
}

func applyRemoteChange(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, path string, contents []byte) error {
	script := "set -eu; path=" + remote.ShellCommand(path) + "; dir=$(dirname \"$path\"); mkdir -p \"$dir\"; if test -f \"$path\" && test ! -e \"$path.nexo.bak\"; then cp -p \"$path\" \"$path.nexo.bak\"; fi; tmp=$(mktemp \"$dir/.nexo-setup.XXXXXX\"); trap 'rm -f \"$tmp\"' EXIT; cat >\"$tmp\"; if test -f \"$path\"; then chmod \"$(stat -c %a \"$path\" 2>/dev/null || stat -f %Lp \"$path\")\" \"$tmp\" 2>/dev/null || true; fi; mv -f \"$tmp\" \"$path\""
	_, err := runInput(ctx, commandRunner, machine, sshConfig, contents, "sh", "-c", script)
	return err
}

func writeLine(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}

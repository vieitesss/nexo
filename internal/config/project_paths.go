package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/vieitesss/nexo/internal/destination"
)

// AddProjectParent registers the parent of destination in project discovery.
// It is the shared add-parent operation used by clone and worktree flows.
func AddProjectParent(path, destinationPath, home string) (restore func() error, changed bool, err error) {
	if path == "" {
		path = DefaultPath()
	}
	if strings.TrimSpace(destinationPath) == "" {
		return nil, false, errors.New("destination path is empty")
	}
	destinationPath = destination.ExpandHome(destinationPath, home)
	absolute, err := filepath.Abs(destinationPath)
	if err != nil {
		return nil, false, fmt.Errorf("resolve destination %q: %w", destinationPath, err)
	}
	return AddProjectPath(path, filepath.Dir(absolute), home)
}

// AddProjectPath appends parent to the top-level paths array without
// reformatting the rest of the configuration. The returned restore function
// only restores when the file still contains this operation's exact write.
func AddProjectPath(path, parent, home string) (restore func() error, changed bool, err error) {
	if path == "" {
		return nil, false, errors.New("config path is empty")
	}
	if parent == "" {
		return nil, false, errors.New("project parent path is empty")
	}

	original, mode, existed, err := readConfigFile(path)
	if err != nil {
		return nil, false, err
	}
	if existed {
		var cfg Config
		if err := decode(original, path, &cfg); err != nil {
			return nil, false, err
		}
		if destination.Covered(cfg.Paths, parent, home) {
			return func() error { return nil }, false, nil
		}
	}

	updated, err := appendProjectPath(original, parent)
	if err != nil {
		return nil, false, fmt.Errorf("update config %s: %w", path, err)
	}
	if bytes.Equal(updated, original) && existed {
		return func() error { return nil }, false, nil
	}
	if err := writeConfigFile(path, updated, mode, existed); err != nil {
		return nil, false, err
	}
	writtenInfo, statErr := os.Stat(path)
	if statErr != nil {
		return nil, false, fmt.Errorf("stat updated config %s: %w", path, statErr)
	}

	restore = func() error {
		currentInfo, currentStatErr := os.Stat(path)
		if currentStatErr != nil {
			if !existed && errors.Is(currentStatErr, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("check config before restore: %w", currentStatErr)
		}
		if !os.SameFile(writtenInfo, currentInfo) {
			return errors.New("config changed after project path update; refusing to overwrite newer edits")
		}
		current, readErr := os.ReadFile(path)
		if readErr != nil {
			if !existed && errors.Is(readErr, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("check config before restore: %w", readErr)
		}
		if !bytes.Equal(current, updated) {
			return errors.New("config changed after project path update; refusing to overwrite newer edits")
		}
		if existed {
			return writeConfigFile(path, original, mode, true)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return restore, true, nil
}

func readConfigFile(path string) (contents []byte, mode os.FileMode, existed bool, err error) {
	info, statErr := os.Stat(path)
	if statErr == nil {
		contents, err = os.ReadFile(path)
		if err != nil {
			return nil, 0, true, fmt.Errorf("read config %s: %w", path, err)
		}
		return contents, info.Mode().Perm(), true, nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return nil, 0, false, fmt.Errorf("stat config %s: %w", path, statErr)
	}
	return nil, 0o600, false, nil
}

func writeConfigFile(path string, contents []byte, mode os.FileMode, _ bool) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".config.toml-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("preserve config mode: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		cleanup()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	if dirFile, err := os.Open(directory); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func appendProjectPath(contents []byte, parent string) ([]byte, error) {
	quoted := strconv.Quote(parent)
	start, end, found, err := findTopLevelPathsArray(contents)
	if err != nil {
		return nil, err
	}
	if !found {
		insertAt := start
		var result bytes.Buffer
		result.Write(contents[:insertAt])
		if insertAt > 0 && contents[insertAt-1] != '\n' {
			result.WriteByte('\n')
		}
		result.WriteString("paths = [")
		result.WriteString(quoted)
		result.WriteString("]\n")
		result.Write(contents[insertAt:])
		return result.Bytes(), nil
	}
	return appendArrayValue(contents, start, end, quoted), nil
}

func findTopLevelPathsArray(contents []byte) (start, end int, found bool, err error) {
	parser := unstable.Parser{}
	parser.Reset(contents)
	firstTableOffset := len(contents)
	inTable := false
	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table, unstable.ArrayTable:
			inTable = true
			if firstTableOffset == len(contents) {
				key := expression.Key()
				if key.Next() {
					firstTableOffset = int(key.Node().Raw.Offset)
					if lineStart := bytes.LastIndexByte(contents[:firstTableOffset], '\n'); lineStart >= 0 {
						firstTableOffset = lineStart + 1
					} else {
						firstTableOffset = 0
					}
				}
			}
			continue
		case unstable.KeyValue:
			if inTable {
				continue
			}
			key := expression.Key()
			if !key.Next() || !key.IsLast() || string(key.Node().Data) != "paths" {
				continue
			}
			value := expression.Value()
			if value.Kind != unstable.Array {
				return 0, 0, false, errors.New("paths must be an array")
			}
			valueStart := int(key.Node().Raw.Offset + key.Node().Raw.Length)
			for valueStart < len(contents) && contents[valueStart] != '=' {
				valueStart++
			}
			if valueStart < len(contents) {
				valueStart++
			}
			for valueStart < len(contents) && (contents[valueStart] == ' ' || contents[valueStart] == '\t' || contents[valueStart] == '\r' || contents[valueStart] == '\n') {
				valueStart++
			}
			if valueStart >= len(contents) || contents[valueStart] != '[' {
				return 0, 0, false, errors.New("paths array range is unavailable")
			}
			valueEnd, ok := matchingArrayEnd(contents, valueStart)
			if !ok {
				return 0, 0, false, errors.New("paths array is not closed")
			}
			return valueStart, valueEnd, true, nil
		}
	}
	if parser.Error() != nil {
		return 0, 0, false, parser.Error()
	}
	return firstTableOffset, 0, false, nil
}

func stripTOMLComment(line []byte) []byte {
	for i := 0; i < len(line); {
		if next, ok := skipTOMLString(line, i); ok {
			i = next
			continue
		}
		if line[i] == '#' {
			return line[:i]
		}
		i++
	}
	return line
}

func skipTOMLString(contents []byte, start int) (int, bool) {
	if start >= len(contents) || (contents[start] != '"' && contents[start] != '\'') {
		return start, false
	}
	quote := contents[start]
	triple := start+2 < len(contents) && contents[start+1] == quote && contents[start+2] == quote
	if triple {
		for i := start + 3; i+2 < len(contents); i++ {
			if quote == '"' && contents[i] == '\\' {
				i++
				continue
			}
			if contents[i] == quote && contents[i+1] == quote && contents[i+2] == quote {
				return i + 3, true
			}
		}
		return len(contents), true
	}
	for i := start + 1; i < len(contents); i++ {
		if quote == '"' && contents[i] == '\\' {
			i++
			continue
		}
		if contents[i] == quote {
			return i + 1, true
		}
	}
	return len(contents), true
}

func matchingArrayEnd(contents []byte, start int) (int, bool) {
	depth := 0
	for i := start; i < len(contents); i++ {
		if next, ok := skipTOMLString(contents, i); ok {
			i = next - 1
			continue
		}
		switch contents[i] {
		case '#':
			for i < len(contents) && contents[i] != '\n' {
				i++
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func appendArrayValue(contents []byte, start, end int, quoted string) []byte {
	inside := contents[start+1 : end]
	if !bytes.Contains(inside, []byte{'\n'}) {
		trimmedEnd := end
		for trimmedEnd > start+1 && unicode.IsSpace(rune(contents[trimmedEnd-1])) {
			trimmedEnd--
		}
		separator := ""
		if trimmedEnd > start+1 {
			separator = ", "
		}
		result := make([]byte, 0, len(contents)+len(quoted)+len(separator))
		result = append(result, contents[:trimmedEnd]...)
		result = append(result, separator...)
		result = append(result, quoted...)
		result = append(result, contents[trimmedEnd:]...)
		return result
	}

	lastNewline := bytes.LastIndexByte(contents[start+1:end], '\n')
	if lastNewline < 0 {
		return contents
	}
	closeLineStart := start + 1 + lastNewline + 1
	valueLineEnd := closeLineStart - 1
	valueLineStart := valueLineEnd
	for valueLineStart > start+1 && contents[valueLineStart-1] != '\n' {
		valueLineStart--
	}
	for valueLineStart > start+1 && strings.TrimSpace(string(stripTOMLComment(contents[valueLineStart:valueLineEnd]))) == "" {
		valueLineEnd = valueLineStart - 1
		valueLineStart = valueLineEnd
		for valueLineStart > start+1 && contents[valueLineStart-1] != '\n' {
			valueLineStart--
		}
	}

	indent := "  "
	if valueLineStart < valueLineEnd {
		line := contents[valueLineStart:valueLineEnd]
		indent = string(line[:len(line)-len(strings.TrimLeft(string(line), " \t"))])
		if len(indent) == 0 {
			indent = "  "
		}
	}
	if valueLineStart >= valueLineEnd {
		result := make([]byte, 0, len(contents)+len(indent)+len(quoted)+3)
		result = append(result, contents[:closeLineStart]...)
		result = append(result, indent...)
		result = append(result, quoted...)
		result = append(result, ',', '\n')
		result = append(result, contents[closeLineStart:]...)
		return result
	}

	lineWithoutComment := stripTOMLComment(contents[valueLineStart:valueLineEnd])
	valueEnd := valueLineStart + len(lineWithoutComment)
	for valueEnd > valueLineStart && unicode.IsSpace(rune(contents[valueEnd-1])) {
		valueEnd--
	}
	needsComma := valueEnd > valueLineStart && contents[valueEnd-1] != ',' && contents[valueEnd-1] != '['
	commaPos := valueEnd
	result := make([]byte, 0, len(contents)+len(indent)+len(quoted)+5)
	result = append(result, contents[:commaPos]...)
	if needsComma {
		result = append(result, ',')
	}
	result = append(result, contents[commaPos:valueLineEnd]...)
	result = append(result, '\n')
	result = append(result, indent...)
	result = append(result, quoted...)
	result = append(result, ',', '\n')
	result = append(result, contents[valueLineEnd+1:]...)
	return result
}

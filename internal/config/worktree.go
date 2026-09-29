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
)

// AddProjectPath appends parent to the top-level paths array without
// reformatting the rest of the configuration. The returned restore function
// puts the file back exactly as it was before this call.
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
		if projectPathCovered(cfg.Paths, parent, home) {
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

	restore = func() error {
		if existed {
			return writeConfigFile(path, original, mode, true)
		}
		return os.Remove(path)
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

func writeConfigFile(path string, contents []byte, mode os.FileMode, existed bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, contents, mode); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if existed {
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("preserve config mode: %w", err)
		}
	}
	return nil
}

func projectPathCovered(paths []string, parent, home string) bool {
	parentPath := filepath.Clean(parent)
	if absolute, err := filepath.Abs(parentPath); err == nil {
		parentPath = absolute
	}
	for _, pattern := range paths {
		candidate := expandConfigHome(strings.TrimSpace(pattern), home)
		if strings.HasSuffix(candidate, string(filepath.Separator)+"*") {
			candidate = strings.TrimSuffix(candidate, string(filepath.Separator)+"*")
		}
		candidate = filepath.Clean(candidate)
		if absolute, err := filepath.Abs(candidate); err == nil {
			candidate = absolute
		}
		if candidate == parentPath {
			return true
		}
	}
	return false
}

func expandConfigHome(pattern, home string) string {
	if home == "" {
		return pattern
	}
	switch {
	case pattern == "~", pattern == "~/":
		return home
	case strings.HasPrefix(pattern, "~/"):
		return filepath.Join(home, strings.TrimPrefix(pattern, "~/"))
	case pattern == "$HOME":
		return home
	case strings.HasPrefix(pattern, "$HOME/"):
		return filepath.Join(home, strings.TrimPrefix(pattern, "$HOME/"))
	default:
		return pattern
	}
}

func appendProjectPath(contents []byte, parent string) ([]byte, error) {
	quoted := strconv.Quote(parent)
	start, end, found, err := findTopLevelPathsArray(contents)
	if err != nil {
		return nil, err
	}
	if !found {
		var result bytes.Buffer
		result.Write(contents)
		if len(contents) > 0 && contents[len(contents)-1] != '\n' {
			result.WriteByte('\n')
		}
		result.WriteString("paths = [")
		result.WriteString(quoted)
		result.WriteString("]\n")
		return result.Bytes(), nil
	}
	return appendArrayValue(contents, start, end, quoted), nil
}

func findTopLevelPathsArray(contents []byte) (start, end int, found bool, err error) {
	section := ""
	for lineStart := 0; lineStart < len(contents); {
		lineEnd := bytes.IndexByte(contents[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(contents)
		} else {
			lineEnd += lineStart
		}
		line := contents[lineStart:lineEnd]
		withoutComment := stripTOMLComment(line)
		trimmed := strings.TrimSpace(string(withoutComment))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
		}
		if section == "" {
			keyEnd := strings.IndexByte(trimmed, '=')
			if keyEnd > 0 && strings.TrimSpace(trimmed[:keyEnd]) == "paths" {
				valueOffset := bytes.IndexByte(withoutComment, '=')
				value := withoutComment[valueOffset+1:]
				arrayOffset := bytes.IndexByte(value, '[')
				if arrayOffset < 0 {
					return 0, 0, false, errors.New("paths must be an array")
				}
				start = lineStart + valueOffset + 1 + arrayOffset
				end, ok := matchingArrayEnd(contents, start)
				if !ok {
					return 0, 0, false, errors.New("paths array is not closed")
				}
				return start, end, true, nil
			}
		}
		if lineEnd == len(contents) {
			break
		}
		lineStart = lineEnd + 1
	}
	return 0, 0, false, nil
}

func stripTOMLComment(line []byte) []byte {
	inString := false
	escaped := false
	for i, char := range line {
		switch {
		case inString && escaped:
			escaped = false
		case inString && char == '\\':
			escaped = true
		case char == '"':
			inString = !inString
		case char == '#' && !inString:
			return line[:i]
		}
	}
	return line
}

func matchingArrayEnd(contents []byte, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(contents); i++ {
		char := contents[i]
		switch {
		case inString && escaped:
			escaped = false
		case inString && char == '\\':
			escaped = true
		case char == '"':
			inString = !inString
		case !inString && char == '#':
			for i < len(contents) && contents[i] != '\n' {
				i++
			}
		case !inString && char == '[':
			depth++
		case !inString && char == ']':
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

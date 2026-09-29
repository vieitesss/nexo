// Package config loads nexo's single TOML configuration file.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the typed configuration shared by all features.
//
// Backend is empty when no backend default was configured. "auto" is accepted
// explicitly and means that the environment should be detected.
type Config struct {
	Backend string `toml:"backend"`
}

// Load reads a required configuration file.
func Load(path string) (Config, error) {
	return load(path, false)
}

// LoadOptional reads a configuration file, treating a missing file as an
// empty configuration. This is used for the default path.
func LoadOptional(path string) (Config, error) {
	return load(path, true)
}

func load(path string, optional bool) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg, err := parse(contents, path)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// DefaultPath returns the path selected by the process environment.
func DefaultPath() string {
	return DefaultPathFrom(os.Getenv("HOME"), os.Getenv("XDG_CONFIG_HOME"))
}

// DefaultPathFrom returns XDG_CONFIG_HOME/nexo/config.toml when XDG_CONFIG_HOME
// is set, and otherwise HOME/.config/nexo/config.toml.
func DefaultPathFrom(home, xdgConfigHome string) string {
	base := xdgConfigHome
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "nexo", "config.toml")
}

func parse(contents []byte, path string) (Config, error) {
	var cfg Config
	section := ""
	seenBackend := false
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return Config{}, parseError(path, lineNumber, "invalid table header")
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section == "" {
				return Config{}, parseError(path, lineNumber, "empty table name")
			}
			return Config{}, fmt.Errorf("config %s: unknown key %q (line %d)", path, section, lineNumber)
		}

		keyValue := strings.SplitN(line, "=", 2)
		if len(keyValue) != 2 {
			return Config{}, parseError(path, lineNumber, "expected key = value")
		}
		key := strings.TrimSpace(keyValue[0])
		if key == "" {
			return Config{}, parseError(path, lineNumber, "empty key")
		}
		keyPath := key
		if section != "" {
			keyPath = section + "." + key
		}
		if keyPath != "backend" {
			return Config{}, fmt.Errorf("config %s: unknown key %q (line %d)", path, keyPath, lineNumber)
		}
		if seenBackend {
			return Config{}, parseError(path, lineNumber, "duplicate key backend")
		}
		value, err := parseString(strings.TrimSpace(keyValue[1]))
		if err != nil {
			return Config{}, fmt.Errorf("config %s: invalid value for key %q (line %d): %w", path, keyPath, lineNumber, err)
		}
		if value != "" && value != "auto" && value != "tmux" && value != "herdr" {
			return Config{}, fmt.Errorf("config %s: invalid backend %q for key %q (line %d)", path, value, keyPath, lineNumber)
		}
		cfg.Backend = value
		seenBackend = true
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return cfg, nil
}

func parseString(value string) (string, error) {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return strconv.Unquote(value)
	}
	if value == "auto" || value == "tmux" || value == "herdr" {
		return value, nil
	}
	return "", fmt.Errorf("expected a quoted string")
}

func stripComment(line string) string {
	quoted := false
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
	}
	return line
}

func parseError(path string, line int, message string) error {
	return fmt.Errorf("config %s: %s (line %d)", path, message, line)
}

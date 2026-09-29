// Package config loads nexo's single TOML configuration file.
//
// Each feature owns its configuration type and defaults in a separate file in
// this package, then adds one tagged field to Config.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
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
	if err := decode(contents, path, &cfg); err != nil {
		return Config{}, err
	}
	if err := validateBackend(cfg.Backend); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, "backend")
	}
	return cfg, nil
}

func decode(contents []byte, path string, target any) error {
	err := toml.NewDecoder(bytes.NewReader(contents)).
		DisallowUnknownFields().Decode(target)
	if err == nil {
		return nil
	}

	if strict, ok := err.(*toml.StrictMissingError); ok && len(strict.Errors) > 0 {
		unknown := strict.Errors[0]
		line, _ := unknown.Position()
		keyPath := strings.Join(unknown.Key(), ".")
		return fmt.Errorf("config %s: unknown key %q (line %d)", path, keyPath, line)
	}
	if decoded, ok := err.(*toml.DecodeError); ok {
		line, _ := decoded.Position()
		return fmt.Errorf("config %s: %v (line %d)", path, err, line)
	}
	return fmt.Errorf("config %s: %w", path, err)
}

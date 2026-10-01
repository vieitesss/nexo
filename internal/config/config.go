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
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the typed configuration shared by all features.
//
// Backend is empty when no backend default was configured. "auto" is accepted
// explicitly and means that the environment should be detected.
type Config struct {
	Backend        string `toml:"backend"`
	ProjectsConfig `toml:",inline"`
	Agents         AgentsConfig `toml:"agents"`
	Glance         GlanceConfig `toml:"glance"`
	TUI            TUIConfig    `toml:"tui"`
	Remote         RemoteConfig `toml:"remote"`
	Clone          CloneConfig  `toml:"clone"`
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
	// TOML durations are strings while the runtime config uses time.Duration.
	// Decode through a small wire shape so malformed values can identify their
	// own config key instead of failing as a generic type mismatch.
	var raw struct {
		Backend string `toml:"backend"`
		ProjectsConfig
		Agents AgentsConfig `toml:"agents"`
		Glance GlanceConfig `toml:"glance"`
		TUI    TUIConfig    `toml:"tui"`
		Clone  struct {
			DefaultProtocol string `toml:"default-protocol"`
			SSHPrefix       string `toml:"ssh-prefix"`
			HTTPSPrefix     string `toml:"https-prefix"`
		} `toml:"clone"`
		Remote struct {
			Machines         []string          `toml:"machines"`
			Backends         map[string]string `toml:"backends"`
			SSHConfig        string            `toml:"ssh-config"`
			Transport        string            `toml:"transport"`
			ReconnectInitial *string           `toml:"reconnect-initial"`
			ReconnectMax     *string           `toml:"reconnect-max"`
		} `toml:"remote"`
	}
	if err := decode(contents, path, &raw); err != nil {
		return Config{}, err
	}
	cfg := Config{
		Backend:        raw.Backend,
		ProjectsConfig: raw.ProjectsConfig,
		Agents:         raw.Agents,
		Glance:         raw.Glance,
		TUI:            raw.TUI,
		Clone: CloneConfig{
			DefaultProtocol: raw.Clone.DefaultProtocol,
			SSHPrefix:       raw.Clone.SSHPrefix,
			HTTPSPrefix:     raw.Clone.HTTPSPrefix,
		},
		Remote: RemoteConfig{
			Machines:  raw.Remote.Machines,
			Backends:  raw.Remote.Backends,
			SSHConfig: expandConfigHome(raw.Remote.SSHConfig, os.Getenv("HOME")),
			Transport: raw.Remote.Transport,
		},
	}
	if raw.Remote.ReconnectInitial != nil {
		duration, err := time.ParseDuration(*raw.Remote.ReconnectInitial)
		if err != nil {
			return Config{}, fmt.Errorf("config %s: invalid duration %q for key %q", path, *raw.Remote.ReconnectInitial, "remote.reconnect-initial")
		}
		if duration <= 0 {
			return Config{}, fmt.Errorf("config %s: reconnect-initial must be greater than zero", path)
		}
		cfg.Remote.ReconnectInitial = duration
	}
	if raw.Remote.ReconnectMax != nil {
		duration, err := time.ParseDuration(*raw.Remote.ReconnectMax)
		if err != nil {
			return Config{}, fmt.Errorf("config %s: invalid duration %q for key %q", path, *raw.Remote.ReconnectMax, "remote.reconnect-max")
		}
		if duration <= 0 {
			return Config{}, fmt.Errorf("config %s: reconnect-max must be greater than zero", path)
		}
		cfg.Remote.ReconnectMax = duration
	}
	if err := validateBackend(cfg.Backend); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, "backend")
	}
	if err := validateTUI(cfg.TUI); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, "tui.theme")
	}
	if err := validateAgents(cfg.Agents); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, highlightColorKey)
	}
	if err := validateClone(cfg.Clone); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, "clone.default-protocol")
	}
	if err := validateGlanceFields(cfg.Glance.Fields); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, "glance.fields")
	}
	if err := validateGlanceTint(cfg.Glance.Tint); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, "glance.tint")
	}
	if err := validateRemote(cfg.Remote); err != nil {
		return Config{}, fmt.Errorf("config %s: %w for key %q", path, err, remoteValidationKey(err))
	}
	cfg.Agents = cfg.Agents.WithDefaults()
	cfg.Glance = cfg.Glance.WithDefaults()
	cfg.TUI = cfg.TUI.WithDefaults()
	cfg.Clone = cfg.Clone.WithDefaults()
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

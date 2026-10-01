// Package backend selects the one multiplexer driven by an invocation.
package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

// Kind identifies a backend selection.
type Kind string

const (
	Auto  Kind = "auto"
	Tmux  Kind = "tmux"
	Herdr Kind = "herdr"
	None  Kind = "none"
)

// ParseKind validates a user-provided backend name.
func ParseKind(value string) (Kind, error) {
	kind := Kind(strings.ToLower(strings.TrimSpace(value)))
	switch kind {
	case Auto, Tmux, Herdr:
		return kind, nil
	default:
		return "", fmt.Errorf("invalid backend %q (want auto, tmux, or herdr)", value)
	}
}

// Environment reads process environment variables.
type Environment interface {
	LookupEnv(string) (string, bool)
}

// OSEnvironment reads the current process environment.
type OSEnvironment struct{}

// LookupEnv implements Environment.
func (OSEnvironment) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

// MapEnvironment is a deterministic environment useful in tests.
type MapEnvironment map[string]string

// LookupEnv implements Environment.
func (m MapEnvironment) LookupEnv(key string) (string, bool) {
	value, ok := m[key]
	return value, ok
}

// Selection is the result of backend selection.
type Selection struct {
	Kind         Kind     `json:"backend"`
	Source       string   `json:"source"`
	Reason       string   `json:"reason"`
	Capabilities []string `json:"capabilities"`
	Adapter      any      `json:"-"`
}

// Require returns an error when a command needs a live multiplexer but none
// was detected.
func (s Selection) Require() error {
	if s.Kind == None {
		return errors.New("no live multiplexer detected; run nexo inside tmux or Herdr, or pass --backend")
	}
	return nil
}

// ResolveInput controls backend selection. A non-nil Flag always wins,
// including the explicit value "auto".
type ResolveInput struct {
	Flag   *string
	Config config.Config
	Env    Environment
	TTY    string
	Runner runner.CommandRunner
}

// Resolve applies flag > config > auto-detection.
func Resolve(ctx context.Context, input ResolveInput) (Selection, error) {
	if input.Env == nil {
		input.Env = OSEnvironment{}
	}

	if input.Flag != nil {
		kind, err := ParseKind(*input.Flag)
		if err != nil {
			return Selection{}, err
		}
		if kind != Auto {
			return Selection{Kind: kind, Source: "flag", Reason: fmt.Sprintf("selected by --backend=%s", kind)}, nil
		}
		selected, reason, err := detect(ctx, input.Env, input.TTY, input.Runner)
		if err != nil {
			return Selection{}, err
		}
		return Selection{Kind: selected, Source: "auto-detect", Reason: reason}, nil
	}

	if input.Config.Backend != "" {
		kind, err := ParseKind(input.Config.Backend)
		if err != nil {
			return Selection{}, fmt.Errorf("config backend: %w", err)
		}
		if kind != Auto {
			return Selection{Kind: kind, Source: "config", Reason: fmt.Sprintf("selected by config backend=%s", kind)}, nil
		}
	}

	selected, reason, err := detect(ctx, input.Env, input.TTY, input.Runner)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Kind: selected, Source: "auto-detect", Reason: reason}, nil
}

// Detect identifies the inner multiplexer from environment markers and, when
// nested, the TTY reported by tmux.
func Detect(ctx context.Context, env Environment, tty string, commandRunner runner.CommandRunner) (Selection, error) {
	if env == nil {
		env = OSEnvironment{}
	}
	kind, reason, err := detect(ctx, env, tty, commandRunner)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Kind: kind, Source: "auto-detect", Reason: reason}, nil
}

func detect(ctx context.Context, env Environment, tty string, commandRunner runner.CommandRunner) (Kind, string, error) {
	herdrMarker, herdrReason := herdrEnvironment(env)
	tmuxMarker, tmuxReason := tmuxEnvironment(env)

	switch {
	case herdrMarker && tmuxMarker:
		owns, err := tmuxOwnsTTY(ctx, tty, commandRunner)
		if err != nil {
			return "", "", err
		}
		if owns {
			return Tmux, "detected tmux as the inner multiplexer (controlling TTY)", nil
		}
		return Herdr, "detected Herdr as the inner multiplexer (controlling TTY)", nil
	case herdrMarker:
		return Herdr, herdrReason, nil
	case tmuxMarker:
		return Tmux, tmuxReason, nil
	default:
		return None, "no supported multiplexer detected", nil
	}
}

func herdrEnvironment(env Environment) (bool, string) {
	if value, ok := env.LookupEnv("HERDR_ENV"); ok && value == "1" {
		return true, "detected Herdr via HERDR_ENV=1"
	}
	if value, ok := env.LookupEnv("HERDR_SOCKET_PATH"); ok && value != "" {
		return true, "detected Herdr via HERDR_SOCKET_PATH"
	}
	if value, ok := env.LookupEnv("HERDR_PANE_ID"); ok && value != "" {
		return true, "detected Herdr via HERDR_PANE_ID"
	}
	return false, ""
}

func tmuxEnvironment(env Environment) (bool, string) {
	if value, ok := env.LookupEnv("TMUX"); ok && value != "" {
		return true, "detected tmux via TMUX"
	}
	if value, ok := env.LookupEnv("TMUX_PANE"); ok && value != "" {
		return true, "detected tmux via TMUX_PANE"
	}
	return false, ""
}

func tmuxOwnsTTY(ctx context.Context, tty string, commandRunner runner.CommandRunner) (bool, error) {
	if tty == "" {
		return false, nil
	}
	if commandRunner == nil {
		return false, errors.New("cannot determine inner multiplexer: no command runner")
	}

	commands := [][]string{
		{"list-clients", "-F", "#{client_tty}"},
		{"list-panes", "-a", "-F", "#{pane_tty}"},
	}
	var failures []error
	for _, args := range commands {
		output, err := commandRunner.Run(ctx, "tmux", tmux.UTF8Args(args...)...)
		if err != nil {
			failures = append(failures, fmt.Errorf("tmux %s: %w", args[0], err))
			continue
		}
		for _, line := range strings.Split(string(output), "\n") {
			if filepath.Clean(strings.TrimSpace(line)) == filepath.Clean(tty) {
				return true, nil
			}
		}
	}
	if len(failures) == len(commands) {
		return false, fmt.Errorf("cannot determine inner multiplexer: %w", errors.Join(failures...))
	}
	return false, nil
}

// WithCapabilities attaches the selected backend adapter and reports the
// capabilities it implements. The adapter itself is passed to commands so a
// feature can type-assert the small interfaces it needs.
func (s Selection) WithCapabilities(commandRunner runner.CommandRunner, environments ...Environment) Selection {
	s.Capabilities = []string{}
	s.Adapter = nil
	switch s.Kind {
	case Tmux:
		inside := os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
		if len(environments) > 0 && environments[0] != nil {
			inside = false
			if value, ok := environments[0].LookupEnv("TMUX"); ok && value != "" {
				inside = true
			}
			if value, ok := environments[0].LookupEnv("TMUX_PANE"); ok && value != "" {
				inside = true
			}
		}
		s.Adapter = tmux.New(commandRunner, inside)
	case Herdr:
		s.Adapter = herdr.New(commandRunner)
	}
	s.Capabilities = capabilities.Names(s.Adapter)
	return s
}

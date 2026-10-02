// Package commands contains the subcommand registry and command implementations.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

import (
	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
	"github.com/vieitesss/nexo/internal/startup"
)

// Invocation is the public seam shared by registered subcommands.
type Invocation struct {
	Selection  backend.Selection
	Config     config.Config
	ConfigPath string
	Runner     runner.CommandRunner
	Adapter    any
	// ContainerResolver optionally supplies machine-specific container
	// actions. When absent, place picker commands wrap Adapter as the local
	// machine source.
	ContainerResolver capabilities.ContainerActionResolver
	Stdout            io.Writer
	Stderr            io.Writer
	Stdin             io.Reader
	StartupTrace      *startup.Trace
	JSON              bool
	Args              []string
}

// UsageError reports invalid arguments for a command.
type UsageError struct {
	Err error
}

func (e UsageError) Error() string {
	if e.Err == nil {
		return "invalid command usage"
	}
	return e.Err.Error()
}

func (e UsageError) Unwrap() error { return e.Err }

// HandledError reports a binding outcome that the multiplexer binding handles
// itself. The CLI exits non-zero without printing, so a user binding such as
// `nexo ... remote passthrough C-h || tmux select-pane -L` runs the original
// local action when passthrough is not inside a proxy.
type HandledError struct {
	Reason string
}

func (e HandledError) Error() string { return e.Reason }

// Command is a registered nexo subcommand.
type Command interface {
	Name() string
	Run(context.Context, Invocation) error
}

// Registry maps command names to their implementations.
type Registry struct {
	commands map[string]Command
	aliases  map[string]string
}

var builtinFactories []func() Command

// registerBuiltin keeps each subcommand's registration next to its
// implementation, so adding a feature does not require editing a shared
// dispatch switch.
func registerBuiltin(factory func() Command) {
	builtinFactories = append(builtinFactories, factory)
}

// NewRegistry returns the foundation registry. Feature tasks register their
// own command implementation in the file that owns it.
func NewRegistry() *Registry {
	registry := &Registry{commands: make(map[string]Command), aliases: make(map[string]string)}
	for _, factory := range builtinFactories {
		if err := registry.Register(factory()); err != nil {
			panic(fmt.Sprintf("register builtin command: %v", err))
		}
	}
	if err := validateHelp(registry); err != nil {
		panic(fmt.Sprintf("validate builtin help: %v", err))
	}
	return registry
}

// Register adds a command to the registry.
func (r *Registry) Register(command Command) error {
	if command == nil {
		return errors.New("cannot register a nil command")
	}
	name := command.Name()
	if name == "" {
		return errors.New("cannot register a command without a name")
	}
	if r.commands == nil {
		r.commands = make(map[string]Command)
	}
	if r.aliases == nil {
		r.aliases = make(map[string]string)
	}
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command %q is already registered", name)
	}
	r.commands[name] = command
	if metadata, ok := commandHelp(command); ok {
		for _, alias := range metadata.Aliases {
			if alias == "" || alias == name {
				continue
			}
			if _, exists := r.commands[alias]; exists {
				return fmt.Errorf("command alias %q is already registered", alias)
			}
			if _, exists := r.aliases[alias]; exists {
				return fmt.Errorf("command alias %q is already registered", alias)
			}
			r.aliases[alias] = name
		}
	}
	return nil
}

// Lookup finds a command by its subcommand name.
func (r *Registry) Lookup(name string) (Command, bool) {
	command, ok := r.commands[name]
	if ok {
		return command, true
	}
	canonical, ok := r.aliases[name]
	if !ok {
		return nil, false
	}
	command, ok = r.commands[canonical]
	return command, ok
}

// Names returns registered names in command-line order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		if strings.HasPrefix(name, "__") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

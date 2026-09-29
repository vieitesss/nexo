// Package commands contains the subcommand registry and command implementations.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

import "github.com/vieitesss/nexo/internal/backend"

// Invocation is the public seam shared by registered subcommands.
type Invocation struct {
	Selection backend.Selection
	Stdout    io.Writer
	Stderr    io.Writer
	JSON      bool
	Args      []string
}

// Command is a registered nexo subcommand.
type Command interface {
	Name() string
	Run(context.Context, Invocation) error
}

// Registry maps command names to their implementations.
type Registry struct {
	commands map[string]Command
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
	registry := &Registry{commands: make(map[string]Command)}
	for _, factory := range builtinFactories {
		_ = registry.Register(factory())
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
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command %q is already registered", name)
	}
	r.commands[name] = command
	return nil
}

// Lookup finds a command by its subcommand name.
func (r *Registry) Lookup(name string) (Command, bool) {
	command, ok := r.commands[name]
	return command, ok
}

// Names returns registered names in command-line order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type backendCommand struct{}

func init() {
	registerBuiltin(func() Command { return backendCommand{} })
}

func (backendCommand) Name() string { return "backend" }

func (backendCommand) Run(_ context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("backend command requires stdout")
	}
	if invocation.Selection.Kind == "" {
		return errors.New("backend command requires a backend selection")
	}
	if invocation.Selection.Capabilities == nil {
		invocation.Selection.Capabilities = []string{}
	}
	if invocation.JSON {
		encoder := json.NewEncoder(invocation.Stdout)
		return encoder.Encode(invocation.Selection)
	}
	_, err := fmt.Fprintf(invocation.Stdout, "backend: %s\nreason: %s\nsource: %s\ncapabilities: %s\n", invocation.Selection.Kind, invocation.Selection.Reason, invocation.Selection.Source, formatCapabilities(invocation.Selection.Capabilities))
	return err
}

func formatCapabilities(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return fmt.Sprintf("%v", names)
}

type placeholderCommand struct{ name string }

func init() {
	for _, name := range []string{"list", "open", "agents", "worktree", "remote"} {
		name := name
		registerBuiltin(func() Command { return placeholderCommand{name: name} })
	}
}

func (c placeholderCommand) Name() string { return c.name }

func (c placeholderCommand) Run(_ context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	return fmt.Errorf("%s command is not implemented", c.name)
}

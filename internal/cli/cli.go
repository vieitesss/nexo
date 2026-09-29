// Package cli implements nexo's process-level argument parsing and dispatch.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/commands"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
	"github.com/vieitesss/nexo/internal/version"
)

// Options contains parsed process arguments.
type Options struct {
	Backend        *string
	ConfigPath     string
	ConfigExplicit bool
	JSON           bool
	Help           bool
	Version        bool
	Command        string
	Args           []string
}

// Parse handles global flags before or after the subcommand.
func Parse(args []string) (Options, error) {
	var options Options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if options.Command == "" {
				return Options{}, errors.New("expected a subcommand before --")
			}
			options.Args = append(options.Args, args[i+1:]...)
			break
		}
		switch {
		case arg == "--json":
			options.JSON = true
		case arg == "--help", arg == "-h":
			options.Help = true
		case arg == "--version":
			options.Version = true
		case arg == "--backend":
			value, next, err := nextValue(args, i, "--backend")
			if err != nil {
				return Options{}, err
			}
			i = next
			options.Backend = &value
		case strings.HasPrefix(arg, "--backend="):
			value := strings.TrimPrefix(arg, "--backend=")
			if value == "" {
				return Options{}, errors.New("--backend requires a value")
			}
			options.Backend = &value
		case arg == "--config-file", arg == "-c":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return Options{}, err
			}
			i = next
			options.ConfigPath, options.ConfigExplicit = value, true
		case strings.HasPrefix(arg, "--config-file="):
			value := strings.TrimPrefix(arg, "--config-file=")
			if value == "" {
				return Options{}, errors.New("--config-file requires a value")
			}
			options.ConfigPath, options.ConfigExplicit = value, true
		case strings.HasPrefix(arg, "-") && options.Command == "":
			return Options{}, fmt.Errorf("unknown option %q", arg)
		case options.Command == "":
			options.Command = arg
		default:
			options.Args = append(options.Args, arg)
		}
	}
	if options.Command == "" && !options.Help && !options.Version {
		return Options{}, errors.New("a subcommand is required")
	}
	return options, nil
}

func nextValue(args []string, index int, flag string) (string, int, error) {
	if index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "-") {
		return "", index, fmt.Errorf("%s requires a value", flag)
	}
	return args[index+1], index + 1, nil
}

// Run dispatches one invocation and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWith(args, stdout, stderr, runner.ExecRunner{}, backend.OSEnvironment{}, backend.ControllingTTY())
}

// RunWith is the injectable process seam used by tests and embedding callers.
func RunWith(args []string, stdout, stderr io.Writer, commandRunner runner.CommandRunner, environment backend.Environment, tty string) int {
	return RunWithRegistry(commands.NewRegistry(), args, stdout, stderr, commandRunner, environment, tty)
}

// RunWithRegistry dispatches through a supplied registry. The seam lets tests
// and feature packages verify the complete invocation context without
// reaching into process globals.
func RunWithRegistry(registry *commands.Registry, args []string, stdout, stderr io.Writer, commandRunner runner.CommandRunner, environment backend.Environment, tty string) int {
	if registry == nil {
		writeError(stderr, errors.New("command registry is nil"))
		return 2
	}
	if pickerArgs, ok := commands.PlacePickerArgs(args); ok {
		args = pickerArgs
	}
	options, err := Parse(args)
	if err != nil {
		writeError(stderr, err)
		return 2
	}
	if options.Help {
		printHelp(stdout, registry)
		return 0
	}
	if options.Version {
		printVersion(stdout)
		return 0
	}

	command, ok := registry.Lookup(options.Command)
	if !ok {
		writeError(stderr, fmt.Errorf("unknown subcommand %q", options.Command))
		return 2
	}

	cfg, err := loadConfig(options)
	if err != nil {
		writeError(stderr, err)
		return 2
	}
	selection, err := backend.Resolve(context.Background(), backend.ResolveInput{
		Flag:   options.Backend,
		Config: cfg,
		Env:    environment,
		TTY:    tty,
		Runner: commandRunner,
	})
	if err != nil {
		writeError(stderr, err)
		return 2
	}
	selection = selection.WithCapabilities(commandRunner, environment)
	if err := command.Run(context.Background(), commands.Invocation{
		Selection:  selection,
		Config:     cfg,
		ConfigPath: options.ConfigPath,
		Runner:     commandRunner,
		Adapter:    selection.Adapter,
		Stdout:     stdout,
		Stderr:     stderr,
		JSON:       options.JSON,
		Args:       options.Args,
	}); err != nil {
		writeError(stderr, err)
		return 1
	}
	return 0
}

func loadConfig(options Options) (config.Config, error) {
	if options.ConfigExplicit {
		return config.Load(options.ConfigPath)
	}
	return config.LoadOptional(config.DefaultPath())
}

func writeError(stderr io.Writer, err error) {
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "nexo: %s\n", err)
	}
}

func printVersion(stdout io.Writer) {
	if stdout == nil {
		return
	}
	_, _ = fmt.Fprintf(stdout, "nexo %s\n", version.String())
}

func printHelp(stdout io.Writer, registry *commands.Registry) {
	if stdout == nil {
		return
	}
	commandsText := "backend"
	if registry != nil {
		commandsText = strings.Join(registry.Names(), ", ")
	}
	_, _ = fmt.Fprintln(stdout, "Usage: nexo [flags] <command> [args]")
	_, _ = fmt.Fprintf(stdout, "\nCommands: %s\n", commandsText)
	_, _ = fmt.Fprintln(stdout, "\nFlags: --backend auto|tmux|herdr, -c/--config-file PATH, --json, --version")
}

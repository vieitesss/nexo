package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/vieitesss/nexo/internal/backend"
	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/setup"
)

type setupCommand struct{}

func init()                       { registerBuiltin(func() Command { return setupCommand{} }) }
func (setupCommand) Name() string { return "setup" }

func (setupCommand) Run(ctx context.Context, invocation Invocation) error {
	opts, err := parseSetupArgs(invocation.Args)
	if err != nil {
		return UsageError{Err: err}
	}
	if opts.Check {
		return setup.Run(ctx, setup.Input{Runner: invocation.Runner, Stdout: invocation.Stdout, Stderr: invocation.Stderr, Stdin: invocation.Stdin, Home: homeDir(), TTY: backend.ControllingTTY() != "", SelectedBackend: string(invocation.Selection.Kind), ConfigPath: invocation.ConfigPath}, opts)
	}
	input := setup.Input{Runner: invocation.Runner, Stdout: invocation.Stdout, Stderr: invocation.Stderr, Stdin: invocation.Stdin, Home: homeDir(), TTY: backend.ControllingTTY() != "", SelectedBackend: string(invocation.Selection.Kind), ConfigPath: invocation.ConfigPath}
	initializer, ok := invocation.Adapter.(capabilities.TmuxInitializer)
	if !ok {
		initializer = tmuxbackend.New(invocation.Runner, strings.TrimSpace(os.Getenv("TMUX")) != "" || strings.TrimSpace(os.Getenv("TMUX_PANE")) != "")
	}
	input.LocalTmuxInit = func(initCtx context.Context) error {
		return initializer.Init(initCtx, tmuxbackend.InitInput{ConfigPath: invocation.ConfigPath})
	}
	return setup.Run(ctx, input, opts)
}

func homeDir() string {
	value, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return value
}

func parseSetupArgs(args []string) (setup.Options, error) {
	opts := setup.Options{RemoteConfig: false}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--check":
			opts.Check = true
		case arg == "--dry-run":
			opts.DryRun = true
		case arg == "--local-only":
			opts.LocalOnly = true
		case arg == "--yes", arg == "-y":
			opts.Yes = true
		case arg == "--remote-config":
			opts.RemoteConfig = true
		case arg == "--fix-locale":
			opts.FixLocale = true
		case arg == "--no-remote-config":
			opts.RemoteConfig = false
		case arg == "--machine":
			if i+1 >= len(args) {
				return opts, errors.New("--machine requires an alias")
			}
			i++
			opts.Machines = append(opts.Machines, args[i])
		case strings.HasPrefix(arg, "--machine="):
			value := strings.TrimPrefix(arg, "--machine=")
			if value == "" {
				return opts, errors.New("--machine requires an alias")
			}
			opts.Machines = append(opts.Machines, value)
		case arg == "--backend":
			if i+1 >= len(args) {
				return opts, errors.New("--backend requires a value")
			}
			i++
			opts.Backend = args[i]
		case strings.HasPrefix(arg, "--backend="):
			opts.Backend = strings.TrimPrefix(arg, "--backend=")
		case arg == "--path":
			if i+1 >= len(args) {
				return opts, errors.New("--path requires a directory")
			}
			i++
			opts.Paths = append(opts.Paths, args[i])
		case strings.HasPrefix(arg, "--path="):
			opts.Paths = append(opts.Paths, strings.TrimPrefix(arg, "--path="))
		case arg == "--notify":
			value := true
			opts.Notify = &value
		case arg == "--no-notify":
			value := false
			opts.Notify = &value
		case strings.HasPrefix(arg, "--notify="):
			value, err := strconv.ParseBool(strings.TrimPrefix(arg, "--notify="))
			if err != nil {
				return opts, errors.New("--notify expects true or false")
			}
			opts.Notify = &value
		default:
			return opts, fmt.Errorf("unknown setup option %q", arg)
		}
	}
	return opts, nil
}

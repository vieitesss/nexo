package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/setup"
)

type setupCommand struct{}

func init()                       { registerBuiltin(func() Command { return setupCommand{} }) }
func (setupCommand) Name() string { return "setup" }

func (setupCommand) Help() Help {
	return Help{
		Summary:     "Configure nexo on this machine and its remotes.",
		Usage:       "nexo setup [flags]",
		Description: "Probes the local machine and selected SSH remotes, prints one complete plan, and applies it after confirmation. Use --check to inspect readiness without changing anything.",
		Flags: []FlagHelp{
			{Long: "--backend", Value: "auto|tmux|herdr|rex", Description: "Choose the multiplexer backend."},
			{Long: "--check", Description: "Print local and remote readiness checks without changing anything."},
			{Long: "--dry-run", Description: "Print the setup plan without changing anything."},
			{Long: "--fix-locale", Description: "Repair a remote non-UTF-8 locale when a UTF-8 locale is available."},
			{Long: "--local-only", Description: "Configure only this machine; skip SSH remotes."},
			{Long: "--machine", Value: "ALIAS", Description: "Configure this SSH alias; repeat for several."},
			{Long: "--no-notify", Description: "Disable agent notifications in the configuration."},
			{Long: "--no-remote-config", Description: "Do not copy the local agent configuration to remotes."},
			{Long: "--notify", Value: "BOOL", Description: "Set agent notifications to true or false."},
			{Long: "--path", Value: "DIR", Description: "Add a project directory; repeat for several."},
			{Long: "--remote-config", Description: "Copy the local agent configuration to remotes."},
			{Short: "-y", Long: "--yes", Description: "Apply the plan without asking for confirmation."},
		},
		Examples: []string{"nexo setup", "nexo setup --check", "nexo setup --yes --local-only", "nexo setup --machine devbox --dry-run"},
	}
}

// interactiveTerminal reports whether the process has a terminal on stdin and
// stdout, so setup can show its form and upgrade its live table. It checks the
// file descriptors directly so it also works on macOS, where
// backend.ControllingTTY cannot resolve the terminal path.
func interactiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func (setupCommand) Run(ctx context.Context, invocation Invocation) error {
	opts, err := parseSetupArgs(invocation.Args)
	if err != nil {
		return UsageError{Err: err}
	}
	input := setup.Input{Runner: invocation.Runner, Stdout: invocation.Stdout, Stderr: invocation.Stderr, Stdin: invocation.Stdin, Home: homeDir(), TTY: interactiveTerminal(), SelectedBackend: string(invocation.Selection.Kind), SelectedSource: invocation.Selection.Source, ConfigPath: invocation.ConfigPath}
	if opts.Check {
		return setup.Run(ctx, input, opts)
	}
	initializer, ok := invocation.Adapter.(capabilities.TmuxInitializer)
	if !ok {
		initializer = tmuxbackend.New(invocation.Runner, strings.TrimSpace(os.Getenv("TMUX")) != "" || strings.TrimSpace(os.Getenv("TMUX_PANE")) != "")
	}
	input.LocalTmuxInit = func(initCtx context.Context) error {
		return initializer.Init(initCtx, capabilities.InitInput{ConfigPath: invocation.ConfigPath})
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

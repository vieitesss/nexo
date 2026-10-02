package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxyidentity"
	"github.com/vieitesss/nexo/internal/remote"
)

type remoteCommand struct{}

func newRemoteOrchestrator(invocation Invocation, resolver capabilities.ContainerActionResolver) remote.Orchestrator {
	var proxy capabilities.RemoteProxy
	if candidate, ok := invocation.Adapter.(capabilities.RemoteProxy); ok {
		proxy = candidate
	}
	return remote.NewOrchestrator(invocation.Runner, remote.ListingConfig{
		Backend:   invocation.Selection.Kind,
		SSHConfig: invocation.Config.Remote.SSHConfig,
		Backends:  invocation.Config.Remote.Backends,
	}, remote.OperationPolicy{
		Proxy:    proxy,
		Resolver: resolver,
		Stdout:   invocation.Stdout,
	})
}

func init() {
	registerBuiltin(func() Command { return remoteCommand{} })
}

func (remoteCommand) Name() string { return "remote" }
func (remoteCommand) Help() Help {
	return Help{Summary: "List and operate on containers on remote machines.", Usage: "nexo remote <subcommand>", Subcommands: []SubcommandHelp{
		{Name: "attach", Summary: "Attach the proxy pane to a remote container.", Usage: "nexo remote attach [--from-env] [MACHINE CONTAINER]", Description: "Use MACHINE CONTAINER for tmux, or --from-env inside a Herdr proxy workspace.", Flags: []FlagHelp{{Long: "--from-env", Description: "Read the proxy identity from Herdr environment variables."}}, Examples: []string{"nexo remote attach devbox api", "nexo remote attach --from-env"}, Hidden: true},
		{Name: "close", Summary: "Close a local proxy container.", Usage: "nexo remote close [MACHINE/CONTAINER]"},
		{Name: "list", Summary: "List containers on configured remote machines.", Usage: "nexo remote list"},
		{Name: "new", Summary: "Create a remote container and open its proxy.", Usage: "nexo remote new MACHINE [NAME]"},
		{Name: "open", Summary: "Open a remote container locally or through a proxy.", Usage: "nexo remote open MACHINE/CONTAINER", Description: "Herdr opens the remote container natively; tmux uses a local proxy."},
		{Name: "passthrough", Summary: "Forward keys to a remote container.", Usage: "nexo remote passthrough KEY..."},
		{Name: "paste", Summary: "Upload an image from the clipboard, or pass keys through.", Usage: "nexo remote paste [KEY...]", Description: "When a clipboard image is available it is uploaded and its path is sent to the remote container. Missing or non-image clipboard data falls back to the supplied keys."},
		{Name: "previous", Summary: "Open the previous remote container.", Usage: "nexo remote previous"},
	}, Examples: []string{"nexo remote list", "nexo remote open devbox/api", "nexo remote new devbox feature-api"}}
}

func (remoteCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := validateRemoteSyntax(invocation.Args); err != nil {
		return UsageError{Err: err}
	}
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	switch invocation.Args[0] {
	case "list":
		return runRemoteList(ctx, invocation)
	case "open":
		return runRemoteOpen(ctx, invocation)
	case "new":
		return runRemoteNew(ctx, invocation)
	case "close":
		return runRemoteClose(ctx, invocation)
	case "attach":
		// attach is intentionally hidden from help. It is the command run by
		// the sole pane in a proxy session.
		return runRemoteAttach(ctx, invocation)
	case "paste":
		return runRemotePaste(ctx, invocation)
	case "passthrough":
		return runRemotePassthrough(ctx, invocation)
	case "previous":
		return runRemotePrevious(ctx, invocation)
	default:
		panic("remote syntax validated above")
	}
}

func validateRemoteSyntax(args []string) error {
	if len(args) == 0 {
		return errors.New("remote requires a subcommand (want list, open, new, close, previous, paste, or passthrough)")
	}
	switch args[0] {
	case "list", "previous":
		if len(args) != 1 {
			return fmt.Errorf("remote %s accepts no arguments", args[0])
		}
	case "open":
		if len(args) != 2 {
			return errors.New("remote open requires MACHINE/CONTAINER")
		}
		if _, err := proxyidentity.ParseTarget(args[1]); err != nil {
			return fmt.Errorf("remote open requires MACHINE/CONTAINER: %w", err)
		}
	case "new":
		if len(args) < 2 || len(args) > 3 || strings.TrimSpace(args[1]) == "" {
			return errors.New("remote new requires MACHINE [NAME]")
		}
	case "close":
		if len(args) > 2 {
			return errors.New("remote close accepts at most MACHINE/CONTAINER")
		}
		if len(args) == 2 {
			if _, err := proxyidentity.ParseTarget(args[1]); err != nil {
				return fmt.Errorf("remote close requires MACHINE/CONTAINER: %w", err)
			}
		}
	case "attach":
		if _, err := parseRemoteAttachArgs(args); err != nil {
			return err
		}
	case "paste":
		// Clipboard paste accepts optional fallback keys.
	case "passthrough":
		if len(args) < 2 {
			return errors.New("remote passthrough requires at least one key")
		}
	default:
		return fmt.Errorf("unknown remote subcommand %q (want list, open, new, close, paste, passthrough, or previous)", args[0])
	}
	return nil
}

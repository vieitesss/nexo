package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/vieitesss/nexo/internal/capabilities"
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
		{Name: "attach", Summary: "Attach the proxy pane to a remote container.", Usage: "nexo remote attach MACHINE CONTAINER", Flags: []FlagHelp{{Long: "--from-env", Description: "Read the proxy identity from Herdr environment variables."}}, Hidden: true},
		{Name: "close", Summary: "Close a local proxy container.", Usage: "nexo remote close [MACHINE/CONTAINER]"},
		{Name: "list", Summary: "List containers on configured remote machines.", Usage: "nexo remote list"},
		{Name: "new", Summary: "Create a remote container and open its proxy.", Usage: "nexo remote new MACHINE [NAME]"},
		{Name: "open", Summary: "Open a remote container through a proxy.", Usage: "nexo remote open MACHINE/CONTAINER"},
		{Name: "passthrough", Summary: "Forward keys to a remote container.", Usage: "nexo remote passthrough KEY..."},
		{Name: "paste", Summary: "Paste clipboard content into a remote container.", Usage: "nexo remote paste [KEY...]"},
		{Name: "previous", Summary: "Open the previous remote container.", Usage: "nexo remote previous"},
	}, Examples: []string{"nexo remote list", "nexo remote open devbox/api", "nexo remote new devbox feature-api"}}
}

func (remoteCommand) Run(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) == 0 {
		return UsageError{Err: errors.New("remote requires a subcommand (want list, open, new, close, previous, paste, or passthrough)")}
	}
	switch invocation.Args[0] {
	case "list", "open", "new", "close", "attach", "paste", "passthrough", "previous":
	default:
		return UsageError{Err: fmt.Errorf("unknown remote subcommand %q (want list, open, new, close, paste, passthrough, or previous)", invocation.Args[0])}
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
		return UsageError{Err: fmt.Errorf("unknown remote subcommand %q (want list, open, new, close, paste, passthrough, or previous)", invocation.Args[0])}
	}
}

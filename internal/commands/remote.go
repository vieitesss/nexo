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

func (remoteCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	if len(invocation.Args) == 0 {
		return errors.New("remote requires a subcommand (want list, open, new, close, previous, paste, or passthrough)")
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
		return fmt.Errorf("unknown remote subcommand %q (want list, open, new, close, paste, passthrough, or previous)", invocation.Args[0])
	}
}

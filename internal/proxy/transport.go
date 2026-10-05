package proxy

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

// commandTransport is the process transport for the hidden attach command.
// The backend selects the rendezvous namespace shared with explicit switching.
type commandTransport struct {
	config  config.RemoteConfig
	backend string
}

func (t commandTransport) Attach(ctx context.Context, machine, container string) error {
	args := TransportArgs(t.config, t.backend, machine, container)
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

// NewCommandTransport returns the process transport used by the hidden attach
// command.
func NewCommandTransport(remote config.RemoteConfig, backend string) Transport {
	return commandTransport{config: remote, backend: backend}
}

// TransportArgs builds the argv for one remote attachment through the
// configured transport. The backend is part of the rendezvous key so an
// attachment and an explicit switch from the same backend meet on the same
// remote file.
func TransportArgs(remote config.RemoteConfig, backend, machine, container string) []string {
	transport := remote.EffectiveTransport()
	container = CanonicalContainerName(strings.TrimSpace(container))
	command := RemoteAttachShell(ProxyRendezvousKey(backend, machine), machine, container)
	switch transport {
	case "mosh":
		return []string{"mosh", machine, "--", "sh", "-c", command}
	case "et":
		return []string{"et", machine, "-c", "sh -c " + ShellQuote(command)}
	default:
		// ServerAliveInterval/CountMax let ssh notice a dropped link instead of
		// blocking the pane indefinitely. They apply to the attachment only;
		// listing and other remote commands use plain SSH.
		args := []string{"ssh", "-t", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
		if strings.TrimSpace(remote.SSHConfig) != "" {
			args = append(args, "-F", remote.SSHConfig)
		}
		args = append(args, machine, "sh -c "+ShellQuote(command))
		return args
	}
}

var _ Transport = commandTransport{}

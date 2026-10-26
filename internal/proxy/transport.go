package proxy

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

// commandTransport is the process transport for the hidden attach command.
// The backend selects the rendezvous namespace shared with explicit switching;
// the remote kind selects the remote attach shell.
type commandTransport struct {
	config     config.RemoteConfig
	backend    string
	remoteKind string
}

func (t commandTransport) Attach(ctx context.Context, machine, container string) error {
	args := TransportArgs(t.config, t.backend, t.remoteKind, machine, container)
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

// NewCommandTransport returns the process transport used by the hidden attach
// command.
func NewCommandTransport(remote config.RemoteConfig, backend, remoteKind string) Transport {
	return commandTransport{config: remote, backend: backend, remoteKind: remoteKind}
}

// TransportArgs builds the argv for one remote attachment through the
// configured transport. The backend is part of the rendezvous key so an
// attachment and an explicit switch from the same backend meet on the same
// remote file. The remote kind selects the shell: tmux resolves a session and
// records its client tty, while Herdr focuses an optional workspace once and
// attaches to the server's own client.
func TransportArgs(remote config.RemoteConfig, backend, remoteKind, machine, container string) []string {
	transport := remote.EffectiveTransport()
	container = strings.TrimSpace(container)
	var command string
	if remoteKind == RemoteKindHerdr {
		command = HerdrAttachShell(machine, container)
	} else {
		command = RemoteAttachShell(ProxyRendezvousKey(backend, machine), machine, CanonicalContainerName(container))
	}
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

package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/remote"
)

type remoteCommand struct{}

func init() {
	registerBuiltin(func() Command { return remoteCommand{} })
}

func (remoteCommand) Name() string { return "remote" }

func (remoteCommand) Run(ctx context.Context, invocation Invocation) error {
	if err := invocation.Selection.Require(); err != nil {
		return err
	}
	if len(invocation.Args) == 0 {
		return errors.New("remote requires a subcommand (want list, open, new, or close)")
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
	default:
		return fmt.Errorf("unknown remote subcommand %q (want list, open, new, or close)", invocation.Args[0])
	}
}

func runRemoteList(ctx context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("remote list requires stdout")
	}
	if len(invocation.Args) != 1 {
		return fmt.Errorf("remote list accepts no arguments")
	}
	lister := remote.NewLister(invocation.Runner, invocation.Selection.Kind, invocation.Config.Remote.SSHConfig)
	results := lister.List(ctx, invocation.Config.Remote.Machines)
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(results)
	}
	return writeRemoteResults(invocation.Stdout, results)
}

func runRemoteOpen(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 2 {
		return errors.New("remote open requires MACHINE/CONTAINER")
	}
	machine, container, err := parseRemoteTarget(invocation.Args[1])
	if err != nil {
		return err
	}
	if !configuredMachine(invocation.Config.Remote.Machines, machine) {
		return fmt.Errorf("remote machine %q is not configured", machine)
	}
	actions, err := containerActionResolver(invocation).ResolveContainerActions(ctx, machine)
	if err != nil {
		return err
	}
	return actions.OpenContainer(ctx, container)
}

func runRemoteNew(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) < 2 || len(invocation.Args) > 3 {
		return errors.New("remote new requires MACHINE [NAME]")
	}
	machine := strings.TrimSpace(invocation.Args[1])
	if err := validateRemoteMachine(invocation.Config.Remote.Machines, machine); err != nil {
		return err
	}
	name := ""
	if len(invocation.Args) == 3 {
		name = strings.TrimSpace(invocation.Args[2])
	} else {
		if invocation.Stdout == nil {
			return errors.New("remote new requires a name when stdout is unavailable")
		}
		_, _ = fmt.Fprint(invocation.Stdout, "Remote container name: ")
		input := invocation.Stdin
		if input == nil {
			input = os.Stdin
		}
		line, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && len(line) == 0 {
			return fmt.Errorf("read remote container name: %w", err)
		}
		name = strings.TrimSpace(line)
	}
	if name == "" {
		return errors.New("remote new requires a non-empty name")
	}
	if strings.ContainsAny(name, "\r\n") {
		return errors.New("remote container name must be one line")
	}
	proxy, ok := invocation.Adapter.(capabilities.RemoteProxy)
	if !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	if invocation.Runner == nil {
		return errors.New("remote new requires a command runner")
	}
	args := []string{"-o", "BatchMode=yes"}
	if configPath := strings.TrimSpace(invocation.Config.Remote.SSHConfig); configPath != "" {
		args = append(args, "-F", configPath)
	}
	args = append(args, machine, "tmux new-session -d -s "+remoteShellQuote(name))
	if output, err := invocation.Runner.Run(ctx, "ssh", args...); err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("create remote container: %s", text)
		}
		return fmt.Errorf("create remote container on %s: %w", machine, err)
	}
	return proxy.Proxy(ctx, machine, name)
}

func runRemoteClose(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) > 2 {
		return errors.New("remote close accepts at most MACHINE/CONTAINER")
	}
	if invocation.Runner == nil {
		return errors.New("remote close requires a command runner")
	}
	target := ""
	if len(invocation.Args) == 2 {
		target = invocation.Args[1]
	} else {
		pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
		if pane == "" {
			return errors.New("remote close requires MACHINE/CONTAINER outside a proxy session")
		}
		output, err := invocation.Runner.Run(ctx, "tmux", "display-message", "-p", "-t", pane, "#{session_name}")
		if err != nil {
			return fmt.Errorf("find current proxy: %w", err)
		}
		target = strings.TrimSpace(string(output))
	}
	machine, container, err := parseRemoteTarget(target)
	if err != nil {
		return err
	}
	if !configuredMachine(invocation.Config.Remote.Machines, machine) {
		return fmt.Errorf("remote machine %q is not configured", machine)
	}
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	killer, ok := invocation.Adapter.(capabilities.ContainerKiller)
	if !ok {
		return fmt.Errorf("backend %q does not support closing proxy containers", invocation.Selection.Kind)
	}
	// This is deliberately a local backend operation. It only kills the
	// proxy container named MACHINE/CONTAINER and never sends a close command
	// to the remote machine.
	return killer.KillContainer(ctx, machine+"/"+container)
}

func runRemoteAttach(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 3 {
		return errors.New("remote attach requires MACHINE CONTAINER")
	}
	machine := strings.TrimSpace(invocation.Args[1])
	container := strings.TrimSpace(invocation.Args[2])
	if machine == "" || strings.HasPrefix(machine, "-") {
		return errors.New("remote attach requires a valid machine")
	}
	if container == "" {
		return errors.New("remote attach requires a container")
	}
	initial, maximum := invocation.Config.Remote.ReconnectDelays()
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	return tmux.RunReconnect(ctx, tmux.NewCommandTransport(invocation.Config.Remote), nil, input, machine, container, initial, maximum)
}

func parseRemoteTarget(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	separator := strings.IndexByte(target, '/')
	if separator <= 0 || separator == len(target)-1 {
		return "", "", errors.New("remote target must be MACHINE/CONTAINER")
	}
	machine := strings.TrimSpace(target[:separator])
	container := strings.TrimSpace(target[separator+1:])
	if machine == "" || strings.HasPrefix(machine, "-") {
		return "", "", errors.New("remote target has an invalid machine")
	}
	if container == "" {
		return "", "", errors.New("remote target has an empty container")
	}
	return machine, container, nil
}

func validateRemoteMachine(machines []string, machine string) error {
	if strings.HasPrefix(machine, "-") {
		return fmt.Errorf("machine %q starts with '-'", machine)
	}
	if !configuredMachine(machines, machine) {
		return fmt.Errorf("remote machine %q is not configured", machine)
	}
	return nil
}

func configuredMachine(machines []string, machine string) bool {
	machine = strings.TrimSpace(machine)
	if machine == "" || strings.HasPrefix(machine, "-") {
		return false
	}
	for _, configured := range machines {
		if strings.TrimSpace(configured) == machine {
			return true
		}
	}
	return false
}

func writeRemoteResults(stdout io.Writer, results []remote.MachineResult) error {
	for _, result := range results {
		if result.Error != "" {
			if _, err := fmt.Fprintf(stdout, "%s: %s\n", result.Machine, result.Error); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "%s:\n", result.Machine); err != nil {
			return err
		}
		for _, container := range result.Containers {
			if _, err := fmt.Fprintf(stdout, "  %s\n", container.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func remoteShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

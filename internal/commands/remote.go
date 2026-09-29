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

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/proxy"
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
	result, err := listRemoteMachine(ctx, invocation, machine)
	if err != nil {
		return err
	}
	return openRemoteContainer(ctx, invocation, machine, container, result.Kind)
}

func runRemoteNew(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) < 2 || len(invocation.Args) > 3 {
		return errors.New("remote new requires MACHINE [NAME]")
	}
	machine := strings.TrimSpace(invocation.Args[1])
	if err := validateRemoteMachine(invocation.Config.Remote.Machines, machine); err != nil {
		return err
	}
	name, err := remoteContainerName(invocation)
	if err != nil {
		return err
	}
	result, err := listRemoteMachine(ctx, invocation, machine)
	if err != nil {
		return err
	}
	if result.Kind == remote.KindHerdr {
		if invocation.Selection.Kind != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs Herdr; use Herdr to open it", machine)
		}
		if err := createHerdrRemoteWorkspace(ctx, invocation, machine, name); err != nil {
			return err
		}
		return openRemoteContainer(ctx, invocation, machine, name, result.Kind)
	}
	if err := tmux.ValidateProxyIdentity(machine, name); err != nil {
		return err
	}
	if invocation.Runner == nil {
		return errors.New("remote new requires a command runner")
	}
	args := []string{"-o", "BatchMode=yes"}
	if configPath := strings.TrimSpace(invocation.Config.Remote.SSHConfig); configPath != "" {
		args = append(args, "-F", configPath)
	}
	args = append(args, machine, "tmux new-session -d -P -F '#{session_name}' -s "+remoteShellQuote(tmux.EscapeSessionName(name)))
	output, err := invocation.Runner.Run(ctx, "ssh", args...)
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("create remote container: %s", text)
		}
		return fmt.Errorf("create remote container on %s: %w", machine, err)
	}
	canonical := strings.TrimSpace(string(output))
	if newline := strings.IndexByte(canonical, '\n'); newline >= 0 {
		canonical = canonical[:newline]
	}
	if canonical == "" {
		canonical = tmux.CanonicalContainerName(name)
	}
	proxy, ok := invocation.Adapter.(capabilities.RemoteProxy)
	if !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	return proxy.Proxy(ctx, machine, canonical)
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
	} else if invocation.Selection.Kind == backend.Herdr {
		pane := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
		resolver, ok := invocation.Adapter.(interface {
			ProxyTarget(context.Context, string) (string, error)
		})
		if pane == "" || !ok {
			return errors.New("remote close requires MACHINE/CONTAINER outside a proxy session")
		}
		var err error
		target, err = resolver.ProxyTarget(ctx, pane)
		if err != nil {
			return fmt.Errorf("find current proxy: %w", err)
		}
		if strings.TrimSpace(target) == "" {
			return errors.New("current workspace is not a nexo proxy")
		}
	} else {
		pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
		if pane == "" {
			return errors.New("remote close requires MACHINE/CONTAINER outside a proxy session")
		}
		output, err := invocation.Runner.Run(ctx, "tmux", "display-message", "-p", "-t", pane, "#{@nexo-proxy}")
		if err != nil {
			return fmt.Errorf("find current proxy: %w", err)
		}
		target = strings.TrimSpace(string(output))
		if target == "" {
			return errors.New("current session is not a nexo proxy")
		}
	}
	machine, container, err := parseRemoteTarget(target)
	if err != nil {
		return err
	}
	if !configuredMachine(invocation.Config.Remote.Machines, machine) {
		return fmt.Errorf("remote machine %q is not configured", machine)
	}
	result, err := listRemoteMachine(ctx, invocation, machine)
	if err != nil {
		return err
	}
	if result.Kind == remote.KindHerdr {
		return fmt.Errorf("remote machine %q runs Herdr; there is no local proxy to close", machine)
	}
	container = tmux.CanonicalContainerName(container)
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	killer, ok := invocation.Adapter.(capabilities.ContainerKiller)
	if !ok {
		return fmt.Errorf("backend %q does not support closing proxy containers", invocation.Selection.Kind)
	}
	resolver, ok := invocation.Adapter.(interface {
		ProxySessionID(context.Context, string, string) (string, error)
	})
	if !ok {
		return fmt.Errorf("backend %q does not support resolving proxy sessions", invocation.Selection.Kind)
	}
	// This is deliberately a local backend operation. It only kills the
	// proxy container and never sends a close command to the remote machine.
	sessionID, err := resolver.ProxySessionID(ctx, machine, container)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("proxy %q/%q was not found", machine, container)
	}
	return killer.KillContainer(ctx, sessionID)
}

func runRemoteAttach(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 3 {
		return errors.New("remote attach requires MACHINE CONTAINER")
	}
	machine := strings.TrimSpace(invocation.Args[1])
	if invocation.Selection.Kind != backend.Tmux && invocation.Selection.Kind != backend.Herdr {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	if invocation.Selection.Kind == backend.Herdr && !configuredMachine(invocation.Config.Remote.Machines, machine) {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	container := tmux.CanonicalContainerName(strings.TrimSpace(invocation.Args[2]))
	if err := validateRemoteMachine(invocation.Config.Remote.Machines, machine); err != nil {
		return err
	}
	if container == "" {
		return errors.New("remote attach requires a container")
	}
	transport := tmux.NewCommandTransport(invocation.Config.Remote)
	if invocation.Config.Remote.EffectiveTransport() != "ssh" {
		return transport.Attach(ctx, machine, container)
	}
	initial, maximum := invocation.Config.Remote.ReconnectDelays()
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	return proxy.RunReconnect(ctx, transport, nil, input, machine, container, initial, maximum)
}

func listRemoteMachine(ctx context.Context, invocation Invocation, machine string) (remote.MachineResult, error) {
	if invocation.Runner == nil {
		// Keep the adapter seam useful to callers that already resolved a
		// tmux proxy and do not need a remote probe.
		return remote.MachineResult{Machine: machine, Kind: remote.KindTmux}, nil
	}
	lister := remote.NewLister(invocation.Runner, invocation.Selection.Kind, invocation.Config.Remote.SSHConfig).WithKindDetection()
	result := lister.ListMachine(ctx, machine)
	if result.Error != "" {
		return result, fmt.Errorf("list remote machine %q: %s", machine, result.Error)
	}
	return result, nil
}

func remoteContainerName(invocation Invocation) (string, error) {
	if len(invocation.Args) == 3 {
		name := strings.TrimSpace(invocation.Args[2])
		if name == "" {
			return "", errors.New("remote new requires a non-empty name")
		}
		if strings.ContainsAny(name, "\r\n") {
			return "", errors.New("remote container name must be one line")
		}
		return name, nil
	}
	if invocation.Stdout == nil {
		return "", errors.New("remote new requires a name when stdout is unavailable")
	}
	_, _ = fmt.Fprint(invocation.Stdout, "Remote container name: ")
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", fmt.Errorf("read remote container name: %w", err)
	}
	name := strings.TrimSpace(line)
	if name == "" {
		return "", errors.New("remote new requires a non-empty name")
	}
	if strings.ContainsAny(name, "\r\n") {
		return "", errors.New("remote container name must be one line")
	}
	return name, nil
}

func openRemoteContainer(ctx context.Context, invocation Invocation, machine, container string, kind remote.Kind) error {
	switch kind {
	case remote.KindHerdr:
		if invocation.Selection.Kind != backend.Herdr {
			return fmt.Errorf("unsupported: %s runs Herdr; use Herdr to open it", machine)
		}
		return openNativeHerdrContainer(ctx, invocation, machine, container)
	case remote.KindTmux:
		container = tmux.CanonicalContainerName(container)
		if err := tmux.ValidateProxyIdentity(machine, container); err != nil {
			return err
		}
		proxy, ok := invocation.Adapter.(capabilities.RemoteProxy)
		if !ok {
			return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
		}
		return proxy.Proxy(ctx, machine, container)
	default:
		return fmt.Errorf("remote machine %q has an unknown multiplexer kind", machine)
	}
}

func openNativeHerdrContainer(ctx context.Context, invocation Invocation, machine, container string) error {
	if invocation.Runner == nil {
		return errors.New("opening a Herdr remote requires a command runner")
	}
	output, err := invocation.Runner.Run(ctx, "herdr", "machine", "list", "--json")
	if err != nil {
		return fmt.Errorf("list Herdr machines: %w", err)
	}
	if !herdrMachineSaved(output, machine) {
		if _, err := invocation.Runner.Run(ctx, "herdr", "machine", "add", "--label", machine, machine); err != nil {
			return fmt.Errorf("save Herdr machine %q: %w", machine, err)
		}
		if invocation.Stdout != nil {
			if _, err := fmt.Fprintf(invocation.Stdout, "saved Herdr machine %s\n", machine); err != nil {
				return err
			}
		}
	}
	if invocation.Stdout == nil {
		return nil
	}
	_, err = fmt.Fprintf(invocation.Stdout, "%s is available under %s in Herdr's sidebar\n", container, machine)
	return err
}

func herdrMachineSaved(output []byte, machine string) bool {
	var value any
	if json.Unmarshal(output, &value) == nil && jsonContainsMachine(value, machine) {
		return true
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == machine {
			return true
		}
	}
	return false
}

func jsonContainsMachine(value any, machine string) bool {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			if jsonContainsMachine(item, machine) {
				return true
			}
		}
	case map[string]any:
		for key, item := range current {
			if key == "label" || key == "name" || key == "machine" || key == "target" || key == "ssh_target" || key == "sshTarget" {
				if text, ok := item.(string); ok && strings.TrimSpace(text) == machine {
					return true
				}
			}
			if jsonContainsMachine(item, machine) {
				return true
			}
		}
	}
	return false
}

func createHerdrRemoteWorkspace(ctx context.Context, invocation Invocation, machine, name string) error {
	if invocation.Runner == nil {
		return errors.New("remote new requires a command runner")
	}
	args := []string{"-o", "BatchMode=yes"}
	if configPath := strings.TrimSpace(invocation.Config.Remote.SSHConfig); configPath != "" {
		args = append(args, "-F", configPath)
	}
	args = append(args, machine, "herdr workspace create --label "+remoteShellQuote(name))
	output, err := invocation.Runner.Run(ctx, "ssh", args...)
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("create remote Herdr container: %s", text)
		}
		return fmt.Errorf("create remote Herdr container on %s: %w", machine, err)
	}
	return nil
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

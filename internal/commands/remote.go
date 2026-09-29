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
	"time"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/backend/herdr"
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
	return openRemoteContainerWithResolver(ctx, invocation, machine, container, result.Kind)
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
		return openRemoteContainerWithResolver(ctx, invocation, machine, name, result.Kind)
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
	// Closing is local-only. Do not probe the remote machine: links are often
	// down precisely when a stale local proxy needs to be removed.
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
	fromEnv := len(invocation.Args) == 2 && invocation.Args[1] == "--from-env"
	if !fromEnv && len(invocation.Args) != 3 {
		return errors.New("remote attach requires MACHINE CONTAINER")
	}
	if fromEnv && invocation.Selection.Kind != backend.Herdr {
		return errors.New("remote attach --from-env requires the Herdr backend")
	}
	if invocation.Selection.Kind != backend.Tmux && invocation.Selection.Kind != backend.Herdr {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	if invocation.Selection.Kind == backend.Herdr && !fromEnv {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	if _, ok := invocation.Adapter.(capabilities.RemoteProxy); !ok {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	var machine, container string
	if fromEnv {
		machine = os.Getenv("NEXO_PROXY_MACHINE")
		container = os.Getenv("NEXO_PROXY_CONTAINER")
		if err := herdr.ValidateProxyIdentity(machine, container); err != nil {
			return err
		}
	} else {
		machine = strings.TrimSpace(invocation.Args[1])
		container = tmux.CanonicalContainerName(strings.TrimSpace(invocation.Args[2]))
		if err := validateRemoteMachine(invocation.Config.Remote.Machines, machine); err != nil {
			return err
		}
		if container == "" {
			return errors.New("remote attach requires a container")
		}
	}
	if invocation.Selection.Kind == backend.Herdr && !configuredMachine(invocation.Config.Remote.Machines, machine) {
		return fmt.Errorf("backend %q does not support remote proxy attachment", invocation.Selection.Kind)
	}
	transport := tmux.NewCommandTransport(invocation.Config.Remote)
	if invocation.Config.Remote.EffectiveTransport() != "ssh" {
		attachErr := transport.Attach(ctx, machine, container)
		if fromEnv {
			return finishHerdrProxyAttach(invocation, attachErr)
		}
		return attachErr
	}
	initial, maximum := invocation.Config.Remote.ReconnectDelays()
	input := invocation.Stdin
	if input == nil {
		input = os.Stdin
	}
	attachErr := proxy.RunReconnect(ctx, transport, nil, input, machine, container, initial, maximum)
	if fromEnv {
		return finishHerdrProxyAttach(invocation, attachErr)
	}
	return attachErr
}

func finishHerdrProxyAttach(invocation Invocation, attachErr error) error {
	cleanupErr := closeHerdrProxyWorkspace(invocation)
	if attachErr != nil && cleanupErr != nil {
		return errors.Join(attachErr, cleanupErr)
	}
	if attachErr != nil {
		return attachErr
	}
	return cleanupErr
}

func closeHerdrProxyWorkspace(invocation Invocation) error {
	if invocation.Runner == nil {
		return errors.New("close Herdr proxy workspace requires a command runner")
	}
	paneID := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	if paneID == "" {
		return errors.New("HERDR_PANE_ID is required to close the Herdr proxy workspace")
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := invocation.Runner.Run(cleanupCtx, "herdr", "pane", "get", paneID)
	if err != nil {
		return fmt.Errorf("find Herdr proxy workspace: %w", err)
	}
	workspaceID, err := decodePaneWorkspaceID(output)
	if err != nil {
		return err
	}
	if _, err := invocation.Runner.Run(cleanupCtx, "herdr", "workspace", "close", workspaceID); err != nil {
		return fmt.Errorf("close Herdr proxy workspace %s: %w", workspaceID, err)
	}
	return nil
}

func decodePaneWorkspaceID(output []byte) (string, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	raw := output
	if json.Unmarshal(output, &envelope) == nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Pane struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || strings.TrimSpace(result.Pane.WorkspaceID) == "" {
		return "", errors.New("herdr proxy pane did not report a workspace id")
	}
	return result.Pane.WorkspaceID, nil
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

func openRemoteContainerWithResolver(ctx context.Context, invocation Invocation, machine, container string, kind remote.Kind) error {
	if invocation.ContainerResolver != nil {
		actions, err := invocation.ContainerResolver.ResolveContainerActions(ctx, machine)
		if err != nil {
			return err
		}
		return actions.OpenContainer(ctx, container)
	}
	return openRemoteContainer(ctx, invocation, machine, container, kind)
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
		if _, err := invocation.Runner.Run(ctx, "herdr", "machine", "add", machine, "--label", machine); err != nil {
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
	// Herdr 0.9 returns a direct array of saved machine rows. Match the SSH
	// target, not the display label, and do not treat disabled rows as usable.
	var rows []struct {
		Target  string `json:"target"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		return false
	}
	for _, row := range rows {
		enabled := row.Enabled == nil || *row.Enabled
		if enabled && strings.TrimSpace(row.Target) == machine {
			return true
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

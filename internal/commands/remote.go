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
		Backend:    invocation.Selection.Kind,
		SSHConfig:  invocation.Config.Remote.SSHConfig,
		DetectKind: true,
		Backends:   invocation.Config.Remote.Backends,
	}, remote.OperationPolicy{
		Backend:  invocation.Selection.Kind,
		Proxy:    proxy,
		Resolver: resolver,
		OpenHerdr: func(ctx context.Context, machine, container string) error {
			return openNativeHerdrContainer(ctx, invocation, machine, container)
		},
		CreateTmux: func(ctx context.Context, machine, name string) (string, error) {
			return createRemoteTmuxContainer(ctx, invocation, machine, name)
		},
		CreateHerdr: func(ctx context.Context, machine, name string) error {
			return createHerdrRemoteWorkspace(ctx, invocation, machine, name)
		},
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

func runRemoteList(ctx context.Context, invocation Invocation) error {
	if invocation.Stdout == nil {
		return errors.New("remote list requires stdout")
	}
	if len(invocation.Args) != 1 {
		return fmt.Errorf("remote list accepts no arguments")
	}
	orchestrator := newRemoteOrchestrator(invocation, nil)
	results := orchestrator.ListWithAgents(ctx, invocation.Config.Remote.ConfiguredMachines())
	if invocation.JSON {
		return json.NewEncoder(invocation.Stdout).Encode(results)
	}
	return writeRemoteResults(invocation.Stdout, results)
}

func runRemoteOpen(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 2 {
		return errors.New("remote open requires MACHINE/CONTAINER")
	}
	target, err := proxyidentity.ParseTarget(invocation.Args[1])
	if err != nil {
		return err
	}
	machine, container := target.Machine, target.Container
	if !proxyidentity.NewMachineSet(invocation.Config.Remote.ConfiguredMachines()).Contains(machine) {
		return fmt.Errorf("remote machine %q is not configured", machine)
	}
	orchestrator := newRemoteOrchestrator(invocation, invocation.ContainerResolver)
	result, err := orchestrator.Machine(ctx, machine)
	if err != nil {
		return err
	}
	return orchestrator.Open(ctx, result, container)
}

func runRemoteNew(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) < 2 || len(invocation.Args) > 3 {
		return errors.New("remote new requires MACHINE [NAME]")
	}
	machine := strings.TrimSpace(invocation.Args[1])
	if err := validateRemoteMachine(invocation.Config.Remote.ConfiguredMachines(), machine); err != nil {
		return err
	}
	name, err := remoteContainerName(invocation)
	if err != nil {
		return err
	}
	orchestrator := newRemoteOrchestrator(invocation, nil)
	result, err := orchestrator.Machine(ctx, machine)
	if err != nil {
		return err
	}
	return orchestrator.New(ctx, result, name)
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
		output, err := invocation.Runner.Run(ctx, "tmux", tmux.UTF8Args("display-message", "-p", "-t", pane, "#{@nexo-proxy}")...)
		if err != nil {
			return fmt.Errorf("find current proxy: %w", err)
		}
		target = strings.TrimSpace(string(output))
		if target == "" {
			return errors.New("current session is not a nexo proxy")
		}
	}
	identity, err := proxyidentity.ParseTarget(target)
	if err != nil {
		return err
	}
	machine, container := identity.Machine, identity.Container
	// Closing is local-only. Do not probe the remote machine: links are often
	// down precisely when a stale local proxy needs to be removed.
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

func runRemotePaste(ctx context.Context, invocation Invocation) error {
	paster, ok := invocation.Adapter.(capabilities.RemotePaste)
	if !ok {
		return fmt.Errorf("backend %q does not support remote paste", invocation.Selection.Kind)
	}
	paneID := currentPaneID(invocation.Selection.Kind)
	if paneID == "" {
		return errors.New("remote paste requires the current pane (tmux: bind with TMUX_PANE=#{pane_id})")
	}
	keys := append([]string(nil), invocation.Args[1:]...)
	target, err := paster.ProxyTarget(ctx, paneID)
	if err != nil {
		return fmt.Errorf("find current proxy: %w", err)
	}
	passThrough := func() error {
		return paster.SendPaneKeys(ctx, paneID, keys)
	}
	if strings.TrimSpace(target) == "" {
		return passThrough()
	}
	reader := remote.ClipboardReader{Runner: invocation.Runner}
	png, err := reader.Read(ctx)
	if err != nil {
		var missing *remote.MissingToolError
		if errors.As(err, &missing) {
			if invocation.Stderr != nil {
				_, _ = fmt.Fprintf(invocation.Stderr, "nexo: %s; passing the key through\n", missing)
			}
			return passThrough()
		}
		return err
	}
	if len(png) == 0 {
		return passThrough()
	}
	identity, err := proxyidentity.ParseTarget(target)
	if err != nil {
		return fmt.Errorf("find current proxy: %w", err)
	}
	path, err := remote.UploadPNG(ctx, invocation.Runner, invocation.Config.Remote.SSHConfig, identity.Machine, png)
	if err != nil {
		return err
	}
	return paster.SendPaneText(ctx, paneID, remote.ShellQuotePath(path))
}

func currentPaneID(kind backend.Kind) string {
	if kind == backend.Herdr {
		return strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	}
	return strings.TrimSpace(os.Getenv("TMUX_PANE"))
}

func runRemotePassthrough(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) < 2 {
		return errors.New("remote passthrough requires at least one key")
	}
	paster, ok := invocation.Adapter.(capabilities.RemotePaste)
	if !ok {
		return fmt.Errorf("backend %q does not support remote passthrough", invocation.Selection.Kind)
	}
	paneID := currentPaneID(invocation.Selection.Kind)
	if paneID == "" {
		return errors.New("remote passthrough requires the current pane (tmux: bind with TMUX_PANE=#{pane_id})")
	}
	keys := append([]string(nil), invocation.Args[1:]...)
	target, err := paster.ProxyTarget(ctx, paneID)
	if err != nil || strings.TrimSpace(target) == "" {
		// Outside a proxy the key belongs to the user's local binding. Return
		// a handled status so `nexo ... passthrough KEY || local-action` runs
		// the original action without injecting bytes into the pane.
		return HandledError{Reason: "current pane is not inside a proxy"}
	}
	return paster.SendPaneKeys(ctx, paneID, keys)
}

func runRemotePrevious(ctx context.Context, invocation Invocation) error {
	if len(invocation.Args) != 1 {
		return errors.New("remote previous accepts no arguments")
	}
	proxy, ok := invocation.Adapter.(capabilities.RemoteProxy)
	if !ok {
		return fmt.Errorf("backend %q does not support remote proxies", invocation.Selection.Kind)
	}
	entries, err := recentContainerEntries(ctx, invocation)
	if err != nil {
		return fmt.Errorf("read previous remote container: %w", err)
	}
	configured := proxyidentity.NewMachineSet(invocation.Config.Remote.ConfiguredMachines())
	// The current machine is the proxy's machine inside a proxy and the local
	// machine otherwise. The most recent entry on any other machine wins, so
	// from a proxy the local machine is a candidate like any remote.
	current, err := currentProxyMachine(ctx, invocation)
	if err != nil {
		return fmt.Errorf("identify current proxy: %w", err)
	}
	var live map[string]struct{}
	for _, entry := range entries {
		if !entry.Proxy {
			// Local entries live in this machine's history, so they are only
			// eligible from a proxy, and only while the container exists.
			if current == "" || entry.ID == "" {
				continue
			}
			if live == nil {
				if live, err = liveLocalContainerIDs(ctx, invocation); err != nil {
					return fmt.Errorf("list live containers: %w", err)
				}
			}
			if _, ok := live[entry.ID]; !ok {
				continue
			}
			opener, ok := invocation.Adapter.(capabilities.ContainerOpener)
			if !ok {
				return fmt.Errorf("backend %q does not support opening containers", invocation.Selection.Kind)
			}
			return opener.OpenContainer(ctx, entry.ID)
		}
		if entry.Machine == "" || entry.Name == "" {
			continue
		}
		// Skip the machine we are attached to and any machine that is no longer
		// configured; both are definite reasons not to select the entry.
		if proxyidentity.NormalizeMachineName(entry.Machine) == current || !configured.Contains(entry.Machine) {
			continue
		}
		return proxy.Proxy(ctx, entry.Machine, entry.Name)
	}
	return reportNothingEligible(ctx, invocation, "no previous container on another machine")
}

// reportNothingEligible tells the user why a key-bound command did nothing.
// The multiplexer's status line is preferred because a binding's stderr is
// invisible; without it the message is returned as an ordinary error.
func reportNothingEligible(ctx context.Context, invocation Invocation, message string) error {
	if displayer, ok := invocation.Adapter.(capabilities.MessageDisplayer); ok {
		if err := displayer.DisplayMessage(ctx, "nexo: "+message); err == nil {
			return nil
		}
	}
	return errors.New(message)
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
		container = strings.TrimSpace(invocation.Args[2])
		if err := validateRemoteMachine(invocation.Config.Remote.ConfiguredMachines(), machine); err != nil {
			return err
		}
		if container == "" {
			return errors.New("remote attach requires a container")
		}
	}
	if invocation.Selection.Kind == backend.Herdr && !invocation.Config.Remote.IsMachineConfigured(machine) {
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

func createRemoteTmuxContainer(ctx context.Context, invocation Invocation, machine, name string) (string, error) {
	if err := tmux.ValidateProxyIdentity(machine, name); err != nil {
		return "", err
	}
	if invocation.Runner == nil {
		return "", errors.New("remote new requires a command runner")
	}
	args := []string{"-o", "BatchMode=yes"}
	if configPath := strings.TrimSpace(invocation.Config.Remote.SSHConfig); configPath != "" {
		args = append(args, "-F", configPath)
	}
	args = append(args, machine, "tmux -u new-session -d -P -F '#{session_name}' -s "+remoteShellQuote(tmux.EscapeSessionName(name)))
	output, err := invocation.Runner.Run(ctx, "ssh", args...)
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return "", fmt.Errorf("create remote container: %s", text)
		}
		return "", fmt.Errorf("create remote container on %s: %w", machine, err)
	}
	canonical := strings.TrimSpace(string(output))
	if newline := strings.IndexByte(canonical, '\n'); newline >= 0 {
		canonical = canonical[:newline]
	}
	if canonical == "" {
		canonical = tmux.CanonicalContainerName(name)
	}
	return canonical, nil
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

func validateRemoteMachine(machines []string, machine string) error {
	if strings.HasPrefix(machine, "-") {
		return fmt.Errorf("machine %q starts with '-'", machine)
	}
	if !proxyidentity.NewMachineSet(machines).Contains(machine) {
		return fmt.Errorf("remote machine %q is not configured", machine)
	}
	return nil
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
		if result.Warning != "" {
			if _, err := fmt.Fprintf(stdout, "  warning: %s\n", result.Warning); err != nil {
				return err
			}
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

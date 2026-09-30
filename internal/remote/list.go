// Package remote lists containers owned by configured machines over SSH.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/runner"
)

const DefaultMachineTimeout = 5 * time.Second

// Kind identifies the multiplexer running on a remote machine.
type Kind string

const (
	KindTmux  Kind = "tmux"
	KindHerdr Kind = "herdr"
)

const (
	agentFrameStart      = "__NEXO_AGENTS_START__"
	agentFrameEnd        = "__NEXO_AGENTS_END__"
	remoteKindFrameStart = "__NEXO_REMOTE_KIND__"
)

// MachineResult is one configured machine's listing result. An error does not
// prevent other machines from being listed.
type MachineResult struct {
	Machine    string                   `json:"machine"`
	Kind       Kind                     `json:"kind,omitempty"`
	Containers []capabilities.Container `json:"containers,omitempty"`
	Agents     []capabilities.Agent     `json:"agents,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

// Lister runs the selected backend locally and decodes the detected remote
// kind for each machine's listing.
type Lister struct {
	Runner     runner.CommandRunner
	Backend    backend.Kind
	SSHConfig  string
	Timeout    time.Duration
	DetectKind bool
}

// NewLister creates an SSH lister. Backend selection controls local decoding;
// a detected remote kind controls the remote listing command.
func NewLister(commandRunner runner.CommandRunner, selected backend.Kind, sshConfig string) Lister {
	return Lister{
		Runner:    commandRunner,
		Backend:   selected,
		SSHConfig: strings.TrimSpace(sshConfig),
		Timeout:   DefaultMachineTimeout,
	}
}

// WithKindDetection enables the read-only remote multiplexer probe in the
// listing round trip. The result carries the detected kind for reuse by an
// opening operation, so no second probe is needed.
func (l Lister) WithKindDetection() Lister {
	l.DetectKind = true
	return l
}

// ListMachine queries one machine. The timeout bounds both SSH connection and
// the remote multiplexer command so a stalled machine cannot hang a picker.
func (l Lister) ListMachine(ctx context.Context, machine string) MachineResult {
	machine = strings.TrimSpace(machine)
	result := MachineResult{Machine: machine}
	if machine == "" {
		result.Error = "machine name is empty"
		return result
	}
	if l.Runner == nil {
		result.Error = "remote listing requires a command runner"
		return result
	}
	if l.Backend != backend.Tmux && l.Backend != backend.Herdr {
		result.Error = fmt.Sprintf("backend %q does not support remote listing", l.Backend)
		return result
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = DefaultMachineTimeout
	}
	machineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := l.run(machineCtx, machine)
	if err != nil {
		if errors.Is(machineCtx.Err(), context.DeadlineExceeded) {
			result.Error = fmt.Sprintf("timed out after %s", timeout)
		} else {
			result.Error = err.Error()
		}
		result.Error = normalizeError(result.Error)
		if result.Error == "" {
			result.Error = "remote listing failed"
		}
		return result
	}
	kind, listing := splitRemoteKind(output, l.Backend)
	result.Kind = kind
	var containers []capabilities.Container
	switch kind {
	case KindTmux:
		containers, err = decodeTmux(listing, machine)
	case KindHerdr:
		containers, err = decodeHerdr(listing, machine)
	}
	if err != nil {
		result.Error = normalizeError(err.Error())
		return result
	}
	result.Containers = containers
	return result
}

// ListMachineWithAgents lists containers and, when available, the agent
// statuses from the same machine. An unavailable remote nexo binary is a
// best-effort omission: containers remain usable and no error is reported.
func (l Lister) ListMachineWithAgents(ctx context.Context, machine string) MachineResult {
	machine = strings.TrimSpace(machine)
	result := MachineResult{Machine: machine}
	if machine == "" {
		result.Error = "machine name is empty"
		return result
	}
	if l.Runner == nil {
		result.Error = "remote listing requires a command runner"
		return result
	}
	if l.Backend != backend.Tmux && l.Backend != backend.Herdr {
		result.Error = fmt.Sprintf("backend %q does not support remote listing", l.Backend)
		return result
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = DefaultMachineTimeout
	}
	machineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := l.runWithAgents(machineCtx, machine)
	if err != nil {
		if errors.Is(machineCtx.Err(), context.DeadlineExceeded) {
			result.Error = fmt.Sprintf("timed out after %s", timeout)
		} else {
			result.Error = normalizeError(err.Error())
		}
		if result.Error == "" {
			result.Error = "remote listing failed"
		}
		return result
	}
	kind, listing := splitRemoteKind(output, l.Backend)
	result.Kind = kind
	containerOutput, agentOutput := splitAgentFrame(listing)
	var containers []capabilities.Container
	switch kind {
	case KindTmux:
		containers, err = decodeTmux(containerOutput, machine)
	case KindHerdr:
		containers, err = decodeHerdr(containerOutput, machine)
	}
	if err != nil {
		result.Error = normalizeError(err.Error())
		return result
	}
	result.Containers = containers
	if len(strings.TrimSpace(string(agentOutput))) > 0 {
		// Agent discovery is optional. Invalid output, an old remote binary, or
		// a backend mismatch must not hide otherwise valid containers.
		if agents, agentErr := decodeAgents(agentOutput); agentErr == nil {
			result.Agents = agents
		}
	}
	return result
}

// List queries all configured machines in parallel. Results retain the input
// order while each machine's own timeout controls completion.
func (l Lister) List(ctx context.Context, machines []string) []MachineResult {
	l.DetectKind = true
	machines = uniqueMachines(machines)
	results := make([]MachineResult, len(machines))
	var wg sync.WaitGroup
	wg.Add(len(machines))
	for index, machine := range machines {
		go func(index int, machine string) {
			defer wg.Done()
			results[index] = l.ListMachine(ctx, machine)
		}(index, machine)
	}
	wg.Wait()
	return results
}

func uniqueMachines(machines []string) []string {
	seen := make(map[string]bool, len(machines))
	unique := make([]string, 0, len(machines))
	for _, machine := range machines {
		machine = strings.TrimSpace(machine)
		if machine == "" || seen[machine] {
			continue
		}
		seen[machine] = true
		unique = append(unique, machine)
	}
	return unique
}

func (l Lister) run(ctx context.Context, machine string) ([]byte, error) {
	if !l.DetectKind {
		return l.runCommand(ctx, machine, l.backendCommand())
	}
	return l.runCommand(ctx, machine, l.remoteListingCommand(false))
}

func (l Lister) runWithAgents(ctx context.Context, machine string) ([]byte, error) {
	if !l.DetectKind {
		return l.runCommand(ctx, machine, l.backendAgentsCommand())
	}
	return l.runCommand(ctx, machine, l.remoteListingCommand(true))
}

// remoteListingCommand probes the remote server and emits its kind beside the
// one listing gathered in this SSH round trip. A running Herdr server wins;
// tmux is the fallback for machines that do not expose Herdr.
func (l Lister) remoteListingCommand(withAgents bool) string {
	herdrStatus := ShellCommand("herdr", "status", "server", "--json")
	herdrList := ShellCommand("herdr", "workspace", "list")
	tmuxList := l.backendTmuxCommand()
	herdrAgents := ShellCommand("nexo", "--backend", string(backend.Herdr), "agents", "--json")
	tmuxAgents := ShellCommand("nexo", "--backend", string(backend.Tmux), "agents", "--json")
	agentFrames := ""
	if withAgents {
		agentFrames = "; printf '\\n%s\\n' " + ShellCommand(agentFrameStart) + "; AGENT_COMMAND 2>/dev/null || true; printf '\\n%s\\n' " + ShellCommand(agentFrameEnd)
	}
	// Herdr status exits successfully even when no server is running. Parse its
	// running field before selecting the one authoritative listing command.
	return strings.Join([]string{
		"herdr_running=0",
		"herdr_status=$(" + herdrStatus + " 2>/dev/null) || herdr_status=''",
		"herdr_compact=$(printf '%s' \"$herdr_status\" | tr -d '[:space:]')",
		"case \"$herdr_compact\" in *'\"running\":true'*) herdr_running=1;; esac",
		"listing_status=0",
		"if test $herdr_running -eq 1; then listing_output=$(" + herdrList + ") || listing_status=$?; printf '%s\\n' " + ShellCommand(remoteKindFrameStart+string(KindHerdr)) + "; printf '%s\\n' \"$listing_output\"" + strings.ReplaceAll(agentFrames, "AGENT_COMMAND", herdrAgents) + "; else listing_output=$(" + tmuxList + ") || listing_status=$?; printf '%s\\n' " + ShellCommand(remoteKindFrameStart+string(KindTmux)) + "; printf '%s\\n' \"$listing_output\"" + strings.ReplaceAll(agentFrames, "AGENT_COMMAND", tmuxAgents) + "; fi",
		"exit $listing_status",
	}, "; ")
}

func (l Lister) backendTmuxCommand() string {
	return ShellCommand("tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}")
}

func (l Lister) runCommand(ctx context.Context, machine, command string) ([]byte, error) {
	args := l.sshArgs(machine, command)
	output, err := l.Runner.Run(ctx, "ssh", args...)
	if err != nil {
		if isNoServer(err, output) {
			// Detection emits the kind before tmux reports its no-server status.
			// Preserve that frame so a stopped tmux machine is a successful empty
			// listing rather than an incorrectly decoded Herdr result.
			if l.DetectKind && bytesHasRemoteKind(output) {
				return output, nil
			}
			return []byte{}, nil
		}
		if text := strings.TrimSpace(string(output)); text != "" {
			return nil, fmt.Errorf("ssh %s: %s", machine, text)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("ssh %s: %s", machine, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("ssh %s: %w", machine, err)
	}
	return output, nil
}

func (l Lister) backendCommand() string {
	if l.Backend == backend.Tmux {
		return ShellCommand("tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}")
	}
	return ShellCommand("herdr", "workspace", "list")
}

func (l Lister) backendAgentsCommand() string {
	listing := l.backendCommand()
	agents := ShellCommand("nexo", "--backend", string(l.Backend), "agents", "--json")
	return strings.Join([]string{
		"agent_status=0",
		listing + " || agent_status=$?",
		"printf '\\n%s\\n' " + ShellCommand(agentFrameStart),
		agents + " 2>/dev/null || true",
		"printf '\\n%s\\n' " + ShellCommand(agentFrameEnd),
		"exit $agent_status",
	}, "; ")
}

func splitRemoteKind(output []byte, fallback backend.Kind) (Kind, []byte) {
	kind := KindTmux
	if fallback == backend.Herdr {
		kind = KindHerdr
	}
	lines := strings.Split(string(output), "\n")
	// The kind marker is control data and is valid only as the first non-empty
	// line emitted by our probe. Never scan listing or optional agent payload.
	for index, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" && index == 0 {
			continue
		}
		if strings.HasPrefix(line, remoteKindFrameStart) {
			switch strings.TrimPrefix(line, remoteKindFrameStart) {
			case string(KindHerdr):
				kind = KindHerdr
			case string(KindTmux):
				kind = KindTmux
			}
			lines = append(lines[:index], lines[index+1:]...)
		}
		break
	}
	return kind, []byte(strings.Join(lines, "\n"))
}

func bytesHasRemoteKind(output []byte) bool {
	text := string(output)
	return strings.Contains(text, remoteKindFrameStart+string(KindTmux)) || strings.Contains(text, remoteKindFrameStart+string(KindHerdr))
}

func splitAgentFrame(output []byte) (listing, agents []byte) {
	lines := strings.Split(string(output), "\n")
	start, end := -1, -1
	for index, line := range lines {
		switch strings.TrimSuffix(line, "\r") {
		case agentFrameStart:
			if start < 0 {
				start = index
			}
		case agentFrameEnd:
			if start >= 0 && end < 0 {
				end = index
			}
		}
	}
	if start < 0 {
		return output, nil
	}
	listing = []byte(strings.Join(lines[:start], "\n"))
	if end < 0 || end <= start {
		return listing, nil
	}
	return listing, []byte(strings.Join(lines[start+1:end], "\n"))
}

// SSHCommandArgs builds the SSH invocation used for remote commands. The
// command is one shell string because SSH executes its trailing arguments on
// the remote shell; ShellCommand quotes every argument before joining it.
func SSHCommandArgs(sshConfig, machine, command string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	if sshConfig = strings.TrimSpace(sshConfig); sshConfig != "" {
		args = append(args, "-F", sshConfig)
	}
	return append(args, machine, command)
}

func (l Lister) sshArgs(machine, command string) []string {
	return SSHCommandArgs(l.SSHConfig, machine, command)
}

func decodeAgents(output []byte) ([]capabilities.Agent, error) {
	var agents []capabilities.Agent
	if err := json.Unmarshal(output, &agents); err != nil {
		return nil, fmt.Errorf("decode remote nexo agents: %w", err)
	}
	return agents, nil
}

func isNoServer(err error, output []byte) bool {
	message := strings.ToLower(string(output) + " " + err.Error())
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message += " " + strings.ToLower(string(exitErr.Stderr))
	}
	return strings.Contains(message, "no server running") || strings.Contains(message, "failed to connect to server")
}

// ShellCommand quotes arguments for execution by a remote POSIX shell.
func ShellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

func normalizeError(message string) string {
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 80 {
			return string(runes[:77]) + "..."
		}
		return line
	}
	return ""
}

func decodeTmux(output []byte, machine string) ([]capabilities.Container, error) {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []capabilities.Container{}, nil
	}
	containers := make([]capabilities.Container, 0)
	for _, line := range strings.Split(trimmed, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		containers = append(containers, capabilities.Container{ID: fields[0], Name: fields[1], Machine: machine})
	}
	return containers, nil
}

type herdrWorkspace struct {
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
}

func decodeHerdr(output []byte, machine string) ([]capabilities.Container, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode remote Herdr workspace list: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Type       string           `json:"type"`
		Workspaces []herdrWorkspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode remote Herdr workspace list: %w", err)
	}
	if result.Type != "" && result.Type != "workspace_list" {
		return nil, fmt.Errorf("decode remote Herdr workspace list: unexpected result type %q", result.Type)
	}
	containers := make([]capabilities.Container, 0, len(result.Workspaces))
	for _, workspace := range result.Workspaces {
		if workspace.ID == "" || workspace.Label == "" {
			continue
		}
		containers = append(containers, capabilities.Container{ID: workspace.ID, Name: workspace.Label, Machine: machine})
	}
	return containers, nil
}

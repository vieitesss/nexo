package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/runner"
)

func newTestOrchestratorWithConfig(commandRunner runner.CommandRunner, config ListingConfig) Orchestrator {
	return NewOrchestrator(commandRunner, config, OperationPolicy{Backend: config.Backend})
}

func newTestOrchestrator(commandRunner runner.CommandRunner, selected backend.Kind, sshConfig string) Orchestrator {
	return newTestOrchestratorWithConfig(commandRunner, ListingConfig{
		Backend:    selected,
		SSHConfig:  sshConfig,
		DetectKind: true,
	})
}

func newTestOrchestratorNoDetection(commandRunner runner.CommandRunner, selected backend.Kind, sshConfig string) Orchestrator {
	return newTestOrchestratorWithConfig(commandRunner, ListingConfig{Backend: selected, SSHConfig: sshConfig})
}

func TestShellCommandQuotesEachArgument(t *testing.T) {
	got := ShellCommand("tmux", "list-sessions", "a'b")
	want := `'tmux' 'list-sessions' 'a'"'"'b'`
	if got != want {
		t.Fatalf("ShellCommand() = %q, want %q", got, want)
	}
}

func TestListMachineUsesSSHBatchModeAndOptionalConfig(t *testing.T) {
	var gotName string
	var gotArgs []string
	orchestrator := newTestOrchestratorNoDetection(fakeRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, append([]string(nil), args...)
		return []byte("$1\tdev\n"), nil
	}), backend.Tmux, "/tmp/ssh config")

	result := listMachine(orchestrator, "devbox")
	if result.Error != "" || len(result.Containers) != 1 || result.Containers[0].Machine != "devbox" {
		t.Fatalf("result = %#v, want one remote container", result)
	}
	if gotName != "ssh" {
		t.Fatalf("runner name = %q, want ssh", gotName)
	}
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-F", "/tmp/ssh config", "devbox", "'tmux' '-u' 'list-sessions' '-F' '#{session_id}\t#{session_name}'"}
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ssh args = %#v, want %#v", gotArgs, want)
	}
}

func TestListMachineUsesExplicitTmuxBackendWithoutProbe(t *testing.T) {
	orchestrator := newTestOrchestratorWithConfig(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := args[len(args)-1]
		if strings.Contains(command, "herdr' 'status") || strings.Contains(command, "herdr' 'workspace") {
			t.Fatalf("remote command = %q, want no Herdr probe", command)
		}
		return []byte(remoteKindFrameStart + "tmux\n$1\tapi\n"), nil
	}), ListingConfig{Backend: backend.Herdr, DetectKind: true, Backends: map[string]string{"devbox": "tmux"}})
	result := listMachine(orchestrator, "devbox")
	if result.Error != "" || result.Kind != KindTmux || len(result.Containers) != 1 || result.Containers[0].Name != "api" {
		t.Fatalf("result = %#v, want explicit tmux listing", result)
	}
}

func TestListMachineUsesExplicitHerdrBackendWithoutProbe(t *testing.T) {
	orchestrator := newTestOrchestratorWithConfig(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := args[len(args)-1]
		if strings.Contains(command, "herdr' 'status") || strings.Contains(command, "tmux' 'list-sessions") {
			t.Fatalf("remote command = %q, want only Herdr listing", command)
		}
		return []byte(remoteKindFrameStart + "herdr\n{\"result\":{\"type\":\"workspace_list\",\"workspaces\":[]}}\n"), nil
	}), ListingConfig{Backend: backend.Tmux, DetectKind: true, Backends: map[string]string{"gpu1": "herdr"}})
	result := listMachine(orchestrator, "gpu1")
	if result.Error != "" || result.Kind != KindHerdr || len(result.Containers) != 0 {
		t.Fatalf("result = %#v, want explicit Herdr listing", result)
	}
}

func TestListMachineWithAgentsUsesConfiguredBackendForAgents(t *testing.T) {
	orchestrator := newTestOrchestratorWithConfig(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := args[len(args)-1]
		if strings.Contains(command, "herdr' 'status") || !strings.Contains(command, "'nexo' '--backend' 'tmux' 'agents' '--json'") {
			t.Fatalf("remote command = %q, want configured tmux agent command without probe", command)
		}
		return []byte(remoteKindFrameStart + "tmux\n$1\tapi\n" + agentFrameStart + "\n[]\n" + agentFrameEnd + "\n"), nil
	}), ListingConfig{Backend: backend.Herdr, DetectKind: true, Backends: map[string]string{"devbox": "tmux"}})
	result := orchestrator.ListMachineWithAgents(context.Background(), "devbox")
	if result.Error != "" || result.Kind != KindTmux || len(result.Containers) != 1 {
		t.Fatalf("result = %#v, want configured tmux listing and agents", result)
	}
}

func TestListMachineWithAgentsReadsRemoteNexoJSONInOnePinnedSSHCommand(t *testing.T) {
	calls := 0
	orchestrator := newTestOrchestrator(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		command := args[len(args)-1]
		if !strings.Contains(command, "--backend") || !strings.Contains(command, "'tmux'") {
			t.Fatalf("remote command = %q, want explicit tmux backend", command)
		}
		return []byte("$1\tapi\n" + agentFrameStart + "\n[{\"container\":\"api\",\"state\":\"stopped\",\"seen\":false}]\n" + agentFrameEnd + "\n"), nil
	}), backend.Tmux, "")
	result := orchestrator.ListMachineWithAgents(context.Background(), "devbox")
	if result.Error != "" || len(result.Containers) != 1 || len(result.Agents) != 1 || result.Agents[0].Container != "api" {
		t.Fatalf("result = %#v, want containers and remote agents", result)
	}
	if calls != 1 {
		t.Fatalf("ssh calls = %d, want one combined listing", calls)
	}
}

func TestListMachineWithAgentsIgnoresMissingRemoteNexo(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := args[len(args)-1]
		if !strings.Contains(command, agentFrameStart) {
			return nil, errors.New("unexpected unframed command")
		}
		return []byte("$1\tapi\n" + agentFrameStart + "\n" + agentFrameEnd + "\n"), nil
	}), backend.Tmux, "")
	result := orchestrator.ListMachineWithAgents(context.Background(), "devbox")
	if result.Error != "" || len(result.Containers) != 1 || len(result.Agents) != 0 {
		t.Fatalf("result = %#v, want containers without dots", result)
	}
}

func TestListMachineWithAgentsReportsInvalidRemoteAgentJSONAsWarning(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("$1\tapi\n" + agentFrameStart + "\nnot-json\n" + agentFrameEnd + "\n"), nil
	}), backend.Tmux, "")
	result := orchestrator.ListMachineWithAgents(context.Background(), "devbox")
	if result.Error != "" || len(result.Containers) != 1 || len(result.Agents) != 0 {
		t.Fatalf("result = %#v, want containers without invalid agent data", result)
	}
	if !strings.HasPrefix(result.Warning, "agents unavailable: decode remote nexo agents:") {
		t.Fatalf("warning = %q, want agent decode warning", result.Warning)
	}
}

func TestListMachineWithAgentsReportsMissingRemoteNexoAsWarning(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("$1\tapi\n" + agentFrameStart + "\n" + agentFrameEnd + "\n" + agentWarningFrameStart + "nexo: command not found\n"), nil
	}), backend.Tmux, "")
	result := orchestrator.ListMachineWithAgents(context.Background(), "devbox")
	if result.Error != "" || len(result.Containers) != 1 || len(result.Agents) != 0 {
		t.Fatalf("result = %#v, want containers without agents", result)
	}
	if result.Warning != "agents unavailable: nexo: command not found" {
		t.Fatalf("warning = %q, want a single agent warning line", result.Warning)
	}
}

func TestRemoteListingCommandsPassTmuxUTF8Flag(t *testing.T) {
	configured := newMachineListing(fakeRunner(func(context.Context, string, ...string) ([]byte, error) { return nil, nil }), ListingConfig{Backend: backend.Tmux, DetectKind: true, Backends: map[string]string{"devbox": "tmux"}})
	if command := configured.remoteListingCommand("devbox", true); !strings.Contains(command, "'tmux' '-u' 'list-sessions'") {
		t.Fatalf("configured command = %q, want tmux -u listing", command)
	}
	auto := newMachineListing(fakeRunner(func(context.Context, string, ...string) ([]byte, error) { return nil, nil }), ListingConfig{Backend: backend.Tmux, DetectKind: true})
	if command := auto.remoteListingCommand("devbox", true); !strings.Contains(command, "'tmux' '-u' 'list-sessions'") {
		t.Fatalf("auto command = %q, want tmux -u listing", command)
	}
}

// TestRemoteAgentShellKeepsListingAndReportsWarning runs the generated remote
// shell for an explicit tmux backend with a stub nexo that fails, then decodes
// the result through the real parser. Containers survive and the failure
// becomes one warning instead of an empty agent list.
func TestRemoteAgentShellKeepsListingAndReportsWarning(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{
		"tmux": "#!/bin/sh\nprintf '$1\\tapi\\n'\n",
		"nexo": "#!/bin/sh\nprintf 'nexo: command not found\\n' >&2\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "TMPDIR=" + t.TempDir()}
	runner := fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := exec.Command("sh", "-c", args[len(args)-1])
		command.Env = env
		return command.Output()
	})
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Tmux, DetectKind: true, Backends: map[string]string{"devbox": "tmux"}}, OperationPolicy{Backend: backend.Tmux})
	result := orchestrator.ListMachineWithAgents(context.Background(), "devbox")
	if result.Error != "" || result.Kind != KindTmux || len(result.Containers) != 1 || result.Containers[0].Name != "api" {
		t.Fatalf("result = %#v, want tmux container from the shell listing", result)
	}
	if result.Warning != "agents unavailable: nexo: command not found" {
		t.Fatalf("warning = %q, want the first stderr line", result.Warning)
	}
}

func TestNonDetectionAgentShellKeepsContainersWhenAgentsFail(t *testing.T) {
	result, tempDir := runNonDetectionAgentShell(t,
		"#!/bin/sh\nprintf '$1\\tapi\\n'\n",
		"#!/bin/sh\nprintf 'nexo: command not found\\n' >&2\nexit 1\n",
	)
	if result.Error != "" || result.Kind != KindTmux || len(result.Containers) != 1 || result.Containers[0].Name != "api" {
		t.Fatalf("result = %#v, want tmux container despite agent failure", result)
	}
	if result.Warning != "agents unavailable: nexo: command not found" {
		t.Fatalf("warning = %q, want the first stderr line", result.Warning)
	}
	if entries, err := os.ReadDir(tempDir); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("agent temp files = %#v, want cleanup", entries)
	}
}

func TestNonDetectionAgentShellPreservesContainerFailure(t *testing.T) {
	result, _ := runNonDetectionAgentShell(t,
		"#!/bin/sh\nprintf '$1\\tapi\\n'\nprintf 'tmux failed\\n' >&2\nexit 7\n",
		"#!/bin/sh\nprintf '[]\\n'\n",
	)
	if result.Error == "" || len(result.Containers) != 0 {
		t.Fatalf("result = %#v, want container listing failure", result)
	}
}

func runNonDetectionAgentShell(t *testing.T, tmuxBody, nexoBody string) (MachineResult, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"tmux": tmuxBody, "nexo": nexoBody} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tempDir := t.TempDir()
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "TMPDIR=" + tempDir}
	runner := fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := exec.Command("sh", "-c", args[len(args)-1])
		command.Env = env
		return command.Output()
	})
	result := newTestOrchestratorNoDetection(runner, backend.Tmux, "").ListMachineWithAgents(context.Background(), "devbox")
	return result, tempDir
}

func TestListMachineWithAgentsPinsHerdrBackend(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := args[len(args)-1]
		if !strings.Contains(command, "'nexo' '--backend' 'herdr' 'agents' '--json'") || !strings.Contains(command, "printf '\\n%s\\n'") {
			t.Fatalf("remote command = %q, want explicit backend and framed output", command)
		}
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"}]}}` + "\n" + agentFrameStart + "\n[]\n" + agentFrameEnd + "\n"), nil
	}), backend.Herdr, "")
	result := orchestrator.ListMachineWithAgents(context.Background(), "gpu1")
	if result.Error != "" || len(result.Containers) != 1 || result.Containers[0].ID != "w1" {
		t.Fatalf("result = %#v, want one Herdr container", result)
	}
}

func TestListMachineTreatsTmuxNoServerStderrAsEmpty(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("no server running on /tmp/tmux-1000/default\n")}
	}), backend.Tmux, "")
	result := listMachine(orchestrator, "devbox")
	if result.Error != "" || len(result.Containers) != 0 {
		t.Fatalf("result = %#v, want empty successful result", result)
	}
}

func TestListMachineNormalizesMultilineErrors(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte("first reason\nsecond reason\n"), errors.New("command failed")
	}), backend.Tmux, "")
	result := listMachine(orchestrator, "devbox")
	if result.Error != "ssh devbox: first reason" {
		t.Fatalf("error = %q, want first normalized line", result.Error)
	}
}

func TestListMachineDecodesHerdrWorkspaces(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"}]}}`), nil
	}), backend.Herdr, "")
	result := listMachine(orchestrator, "gpu1")
	if result.Error != "" || len(result.Containers) != 1 || result.Containers[0].ID != "w1" || result.Containers[0].Machine != "gpu1" {
		t.Fatalf("result = %#v, want one Herdr container", result)
	}
}

func TestListRunsMachinesInParallelAndKeepsFastResultIndependent(t *testing.T) {
	started := make(chan string, 2)
	slowRelease := make(chan struct{})
	orchestrator := NewOrchestrator(fakeRunner(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		machine := args[4]
		started <- machine
		if machine == "slow" {
			select {
			case <-slowRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []byte("$1\t" + machine + "\n"), nil
	}), ListingConfig{Backend: backend.Tmux, DetectKind: true, Timeout: 50 * time.Millisecond}, OperationPolicy{Backend: backend.Tmux})

	done := make(chan []MachineResult, 1)
	go func() { done <- orchestrator.List(context.Background(), []string{"slow", "fast"}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("machines did not start concurrently")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("machines did not start concurrently")
	}
	close(slowRelease)
	select {
	case results := <-done:
		if results[1].Machine != "fast" || len(results[1].Containers) != 1 {
			t.Fatalf("results = %#v, want fast machine result", results)
		}
	case <-time.After(time.Second):
		t.Fatal("listing waited unexpectedly long")
	}
}

func TestListMachineReportsTimeoutAsMachineError(t *testing.T) {
	orchestrator := NewOrchestrator(fakeRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), ListingConfig{Backend: backend.Tmux, DetectKind: true, Timeout: 5 * time.Millisecond}, OperationPolicy{Backend: backend.Tmux})
	result := listMachine(orchestrator, "offline")
	if !strings.Contains(result.Error, "timed out") {
		t.Fatalf("error = %q, want timeout reason", result.Error)
	}
}

type fakeRunner func(context.Context, string, ...string) ([]byte, error)

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

// listMachine is the test-only entry point for a single-machine listing.
func listMachine(orchestrator Orchestrator, machine string) MachineResult {
	return orchestrator.listing.listMachine(context.Background(), machine, false)
}

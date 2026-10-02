package remote

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestHerdrMachineSavedMatchesEnabledSSHTargetOnly(t *testing.T) {
	if !herdrMachineSaved([]byte(`[{"id":"1","label":"other","target":"gpu1","enabled":true}]`), "gpu1") {
		t.Fatal("expected matching target to be recognized")
	}
	if herdrMachineSaved([]byte(`[{"id":"1","label":"gpu1","target":"gpu1","enabled":false}]`), "gpu1") {
		t.Fatal("disabled target must not be recognized")
	}
	if herdrMachineSaved([]byte(`[{"id":"1","label":"gpu1","target":"other","enabled":true}]`), "gpu1") {
		t.Fatal("display label must not identify a machine")
	}
}

func TestOrchestratorOpenUsesDetectedKindAndCanonicalTmuxName(t *testing.T) {
	proxy := &recordingMachineProxy{}
	orchestrator := NewOrchestrator(nil, ListingConfig{Backend: backend.Herdr}, OperationPolicy{
		Proxy: proxy,
	})
	result := MachineResult{Machine: "dev", Kind: KindTmux}
	if err := orchestrator.Open(context.Background(), result, "api.v1:blue"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if proxy.opened != [2]string{"dev", "api_v1_blue"} {
		t.Fatalf("proxy target = %#v, want canonical tmux identity", proxy.opened)
	}
}

func TestOrchestratorNewChoosesNativeCreationFromMachineKind(t *testing.T) {
	var calls []string
	var output bytes.Buffer
	runner := machineRunnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "herdr" && strings.Join(args, " ") == "machine list --json" {
			return []byte("[]"), nil
		}
		return nil, nil
	})
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Herdr}, OperationPolicy{
		Stdout: &output,
	})
	result := MachineResult{Machine: "gpu", Kind: KindHerdr}
	if err := orchestrator.New(context.Background(), result, "api"); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !strings.Contains(strings.Join(calls, "|"), "ssh -o BatchMode=yes gpu herdr workspace create --label 'api'") ||
		!strings.Contains(strings.Join(calls, "|"), "herdr machine add gpu --label gpu") {
		t.Fatalf("calls = %q, want remote creation and native machine setup", calls)
	}
	if !strings.Contains(output.String(), "api is available under gpu") {
		t.Fatalf("output = %q, want native Herdr operation", output.String())
	}
}

func TestOrchestratorFocusAgentUsesRemotePaneAndThenOpenPolicy(t *testing.T) {
	runner := &machineRunner{}
	proxy := &recordingMachineProxy{}
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Tmux, SSHConfig: "/tmp/config"}, OperationPolicy{
		Proxy: proxy,
	})
	result := MachineResult{Machine: "dev", Kind: KindTmux}
	agent := capabilities.Agent{PaneID: "%7", Container: "api"}
	if err := orchestrator.FocusAgent(context.Background(), result, agent); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	if proxy.opened != [2]string{"dev", "api"} {
		t.Fatalf("opened = %#v, want focused agent container", proxy.opened)
	}
	if len(runner.commands) != 2 || !strings.Contains(runner.commands[0], "select-window") || !strings.Contains(runner.commands[1], "select-pane") {
		t.Fatalf("focus commands = %#v, want remote tmux focus", runner.commands)
	}
}

type recordingMachineProxy struct {
	opened [2]string
}

func (p *recordingMachineProxy) Proxy(_ context.Context, machine, container string) error {
	p.opened = [2]string{machine, container}
	return nil
}

type machineRunnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f machineRunnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

type machineRunner struct {
	commands []string
}

func (r *machineRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	return nil, nil
}

var _ capabilities.RemoteProxy = (*recordingMachineProxy)(nil)

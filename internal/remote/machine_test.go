package remote

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestOrchestratorOpenUsesDetectedKindAndCanonicalTmuxName(t *testing.T) {
	proxy := &recordingMachineProxy{}
	orchestrator := NewOrchestrator(nil, ListingConfig{Backend: backend.Herdr}, OperationPolicy{
		Backend: backend.Herdr,
		Proxy:   proxy,
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
	created := ""
	opened := ""
	orchestrator := NewOrchestrator(nil, ListingConfig{Backend: backend.Herdr}, OperationPolicy{
		Backend: backend.Herdr,
		CreateHerdr: func(_ context.Context, machine, name string) error {
			created = machine + "/" + name
			return nil
		},
		OpenHerdr: func(_ context.Context, machine, name string) error {
			opened = machine + "/" + name
			return nil
		},
	})
	result := MachineResult{Machine: "gpu", Kind: KindHerdr}
	if err := orchestrator.New(context.Background(), result, "api"); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if created != "gpu/api" || opened != "gpu/api" {
		t.Fatalf("created = %q, opened = %q, want native Herdr operation", created, opened)
	}
}

func TestOrchestratorFocusAgentUsesRemotePaneAndThenOpenPolicy(t *testing.T) {
	runner := &machineRunner{}
	proxy := &recordingMachineProxy{}
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Tmux, SSHConfig: "/tmp/config"}, OperationPolicy{
		Backend: backend.Tmux,
		Proxy:   proxy,
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

type machineRunner struct {
	commands []string
}

func (r *machineRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, name+" "+strings.Join(args, " "))
	return nil, nil
}

var _ capabilities.RemoteProxy = (*recordingMachineProxy)(nil)

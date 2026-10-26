package remote

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestValidateKindAllowsTheTmuxToHerdrBridgeOnly(t *testing.T) {
	cases := []struct {
		local     backend.Kind
		remote    Kind
		wantError bool
	}{
		{local: backend.Tmux, remote: KindTmux},
		{local: backend.Tmux, remote: KindHerdr},
		{local: backend.Herdr, remote: KindTmux},
		{local: backend.Herdr, remote: KindHerdr},
		{local: "rex", remote: KindTmux, wantError: true},
		{local: "rex", remote: KindHerdr, wantError: true},
		{local: backend.Tmux, remote: "unknown", wantError: true},
	}
	policy := OperationPolicy{}
	for _, test := range cases {
		t.Run(string(test.local)+" to "+string(test.remote), func(t *testing.T) {
			err := policy.validateKind(test.local, "dev", test.remote, "open")
			if test.wantError {
				if err == nil {
					t.Fatal("validateKind() accepted an unsupported pair")
				}
				return
			}
			if err != nil {
				t.Fatalf("validateKind() error = %v, want the pair to be supported", err)
			}
		})
	}
}

func TestOrchestratorOpenBridgesToHerdrAndKeepsHerdrNativeFromHerdr(t *testing.T) {
	bridged := &recordingMachineProxy{}
	orchestrator := NewOrchestrator(nil, ListingConfig{Backend: backend.Tmux}, OperationPolicy{Proxy: bridged})
	result := MachineResult{Machine: "dev", Kind: KindHerdr, Containers: []capabilities.Container{{ID: "w2", Name: "api"}}}
	if err := orchestrator.Open(context.Background(), result, "api"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if bridged.kind != string(KindHerdr) || bridged.opened != [2]string{"dev", "w2"} {
		t.Fatalf("proxy kind = %q opened = %#v, want the Herdr workspace w2", bridged.kind, bridged.opened)
	}

	native := &recordingMachineProxy{}
	savedRunner := machineRunnerFunc(func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "herdr" {
			return []byte(`[{"target":"dev","enabled":true}]`), nil
		}
		return nil, nil
	})
	nativeOrchestrator := NewOrchestrator(savedRunner, ListingConfig{Backend: backend.Herdr}, OperationPolicy{Proxy: native})
	if err := nativeOrchestrator.Open(context.Background(), result, "api"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if native.calls != 0 {
		t.Fatalf("proxy = %#v, want Herdr to keep its native machine path", native.opened)
	}
}

func TestResolveRemoteTargetAcceptsAnIDOrOneUniqueLabel(t *testing.T) {
	result := MachineResult{Machine: "dev", Kind: KindHerdr, Containers: []capabilities.Container{
		{ID: "w1", Name: "api"},
		{ID: "w2", Name: "web"},
	}}
	for _, test := range []struct{ target, want string }{
		{target: "w2", want: "w2"},
		{target: "web", want: "w2"},
		{target: " api ", want: "w1"},
	} {
		got, err := resolveRemoteTarget(backend.Tmux, result, test.target)
		if err != nil || got != test.want {
			t.Fatalf("resolveRemoteTarget(%q) = %q, %v, want %q", test.target, got, err, test.want)
		}
	}
	if _, err := resolveRemoteTarget(backend.Tmux, result, "missing"); err == nil || !strings.Contains(err.Error(), "no workspace missing on dev") {
		t.Fatalf("missing target error = %v, want a no-workspace diagnostic", err)
	}
	if _, err := resolveRemoteTarget(backend.Tmux, result, ""); err == nil {
		t.Fatal("empty target resolved to a workspace")
	}
	// A tmux remote keeps names raw, and a native Herdr session addresses
	// workspaces itself.
	tmuxResult := MachineResult{Machine: "dev", Kind: KindTmux, Containers: []capabilities.Container{{ID: "api"}}}
	if got, err := resolveRemoteTarget(backend.Tmux, tmuxResult, "api.v1"); err != nil || got != "api.v1" {
		t.Fatalf("tmux target = %q, %v, want the raw name", got, err)
	}
	if got, err := resolveRemoteTarget(backend.Herdr, result, "web"); err != nil || got != "web" {
		t.Fatalf("native Herdr target = %q, %v, want the raw name", got, err)
	}
}

func TestResolveRemoteTargetRejectsAnAmbiguousWorkspaceLabel(t *testing.T) {
	result := MachineResult{Machine: "dev", Kind: KindHerdr, Containers: []capabilities.Container{
		{ID: "w1", Name: "api"},
		{ID: "w2", Name: "api"},
	}}
	_, err := resolveRemoteTarget(backend.Tmux, result, "api")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "w1, w2") {
		t.Fatalf("resolveRemoteTarget() error = %v, want an ambiguity diagnostic listing both IDs", err)
	}
}

func TestOrchestratorNewOpensTheWorkspaceIDTheRemoteCreateReported(t *testing.T) {
	proxy := &recordingMachineProxy{}
	runner := machineRunnerFunc(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ssh" {
			return []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w7"}}}`), nil
		}
		return nil, nil
	})
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Tmux}, OperationPolicy{Proxy: proxy})
	if err := orchestrator.New(context.Background(), MachineResult{Machine: "dev", Kind: KindHerdr}, "api"); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if proxy.kind != string(KindHerdr) || proxy.opened != [2]string{"dev", "w7"} {
		t.Fatalf("proxy kind = %q opened = %#v, want the created workspace ID", proxy.kind, proxy.opened)
	}
}

func TestOrchestratorNewFallsBackToTheLabelWhenCreateReportsNoID(t *testing.T) {
	proxy := &recordingMachineProxy{}
	runner := machineRunnerFunc(func(context.Context, string, ...string) ([]byte, error) { return nil, nil })
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Tmux}, OperationPolicy{Proxy: proxy})
	if err := orchestrator.New(context.Background(), MachineResult{Machine: "dev", Kind: KindHerdr}, "api"); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if proxy.kind != string(KindHerdr) || proxy.opened != [2]string{"dev", "api"} {
		t.Fatalf("proxy kind = %q opened = %#v, want the requested label as the fallback target", proxy.kind, proxy.opened)
	}
}

func TestOrchestratorFocusAgentBridgesToTheWorkspaceID(t *testing.T) {
	runner := &machineRunner{}
	proxy := &recordingMachineProxy{}
	orchestrator := NewOrchestrator(runner, ListingConfig{Backend: backend.Tmux}, OperationPolicy{Proxy: proxy})
	agent := capabilities.Agent{PaneID: "w2:api", Container: "api", ContainerID: "w2"}
	if err := orchestrator.FocusAgent(context.Background(), MachineResult{Machine: "dev", Kind: KindHerdr}, agent); err != nil {
		t.Fatalf("FocusAgent() error = %v", err)
	}
	if proxy.kind != string(KindHerdr) || proxy.opened != [2]string{"dev", "w2"} {
		t.Fatalf("proxy kind = %q opened = %#v, want the workspace ID rather than its label", proxy.kind, proxy.opened)
	}
	if len(runner.commands) != 1 || !strings.Contains(runner.commands[0], "'agent' 'focus'") {
		t.Fatalf("commands = %#v, want the remote pane focus before the bridge", runner.commands)
	}
}

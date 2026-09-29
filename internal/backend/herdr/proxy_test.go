package herdr

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestProxyUsesMetadataTokenIdentityAndFocusesExistingWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w-other","label":"machine/container","tokens":{"nexo-proxy":"machine/other"}},{"workspace_id":"w1","label":"renamed","tokens":{"nexo-proxy":"machine/container"}}]}}`),
	}}
	if err := New(runner).Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[1].name != "herdr" || strings.Join(runner.calls[1].args, " ") != "workspace focus w1" {
		t.Fatalf("calls = %#v, want list then focus of token-matched workspace", runner.calls)
	}
}

func TestProxyStoresTokenAndStartsSharedAttachCommand(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`),
		"workspace create --label machine/container --focus": []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"p1"}}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "/tmp/nexo config.toml")
	if err := backend.Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("calls = %#v, want list, create, metadata, and pane input", runner.calls)
	}
	if got := strings.Join(runner.calls[2].args, " "); got != "workspace report-metadata w1 --source nexo --token nexo-proxy=machine/container" {
		t.Fatalf("metadata call = %q", got)
	}
	if runner.calls[3].args[0] != "pane" || runner.calls[3].args[1] != "send-text" || runner.calls[3].args[2] != "p1" || !strings.Contains(runner.calls[3].args[3], "remote attach") {
		t.Fatalf("pane call = %#v, want shared attach command", runner.calls[3])
	}
}

func TestProxyClosesWorkspaceWhenMetadataFails(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`),
			"workspace create --label machine/container --focus": []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"p1"}}}`),
		},
		errors: map[string]error{
			"workspace report-metadata w1 --source nexo --token nexo-proxy=machine/container": context.DeadlineExceeded,
		},
	}
	err := New(runner).Proxy(context.Background(), "machine", "container")
	if err == nil {
		t.Fatal("Proxy() error = nil, want metadata failure")
	}
	if len(runner.calls) != 4 || strings.Join(runner.calls[3].args, " ") != "workspace close w1" {
		t.Fatalf("calls = %#v, want cleanup close", runner.calls)
	}
}

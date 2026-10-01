package herdr

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
)

func TestValidateProxyIdentityRejectsHerdrNormalization(t *testing.T) {
	cases := []string{" machine", "machine ", "container\u0015", "container\u200e"}
	for _, container := range cases {
		if err := ValidateProxyIdentity("machine", container); err == nil {
			t.Fatalf("ValidateProxyIdentity(machine, %q) = nil, want rejection", container)
		}
	}
	if err := ValidateProxyIdentity("machine", strings.Repeat("a", 78)); err == nil {
		t.Fatal("identity over 80 runes was accepted")
	}
}

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

func TestProxyAdoptsConfiguredWorkspaceByExactLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine/container"}]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"machine"}}, "")
	if err := backend.Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 4 || strings.Join(runner.calls[1].args, " ") != "workspace report-metadata w1 --source nexo --token nexo-proxy=machine/container" || strings.Join(runner.calls[3].args, " ") != "workspace focus w1" {
		t.Fatalf("calls = %#v, want metadata adoption then focus", runner.calls)
	}
}

func TestProxyAdoptsBackendOnlyConfiguredWorkspaceByExactLabel(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine/container"}]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Backends: map[string]string{" MACHINE ": "herdr"}}, "")
	if err := backend.Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 4 || strings.Join(runner.calls[1].args, " ") != "workspace report-metadata w1 --source nexo --token nexo-proxy=machine/container" || strings.Join(runner.calls[3].args, " ") != "workspace focus w1" {
		t.Fatalf("calls = %#v, want backend-only machine adoption", runner.calls)
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
	if len(runner.calls) != 5 {
		t.Fatalf("calls = %#v, want list, create, metadata, verification, and pane input", runner.calls)
	}
	if got := strings.Join(runner.calls[2].args, " "); got != "workspace report-metadata w1 --source nexo --token nexo-proxy=machine/container" {
		t.Fatalf("metadata call = %q", got)
	}
	if runner.calls[4].args[0] != "pane" || runner.calls[4].args[1] != "send-text" || runner.calls[4].args[2] != "p1" || !strings.Contains(runner.calls[4].args[3], "remote attach --from-env") || !strings.Contains(runner.calls[4].args[3], "exec ") {
		t.Fatalf("pane call = %#v, want shared attach command", runner.calls[4])
	}
}

func TestProxyDoesNotAdoptLabelTaggedWithAnotherIdentity(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine/container","tokens":{"nexo-proxy":"machine/other"}}]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"machine"}}, "")
	id, err := backend.ProxySessionID(context.Background(), "machine", "container")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if id != "" {
		t.Fatalf("ProxySessionID() = %q, want no adoption of a conflicting label", id)
	}
	for _, c := range runner.calls {
		if strings.HasPrefix(strings.Join(c.args, " "), "workspace report-metadata") {
			t.Fatalf("calls = %#v, want no metadata write", runner.calls)
		}
	}
}

func TestListContainersClassifiesConflictingTokenByTag(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list":           []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine/container","tokens":{"nexo-proxy":"machine/other"}}]}}`),
		"pane list --workspace w1": []byte(`{"result":{"type":"pane_list","panes":[]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"machine"}}, "")
	containers, err := backend.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 1 || !containers[0].Proxy || containers[0].Machine != "machine" || containers[0].Name != "other" {
		t.Fatalf("containers = %#v, want the conflicting token classified as machine/other", containers)
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

func TestSendPaneKeysUsesNativeKeyInput(t *testing.T) {
	runner := &fakeRunner{}
	if err := New(runner).SendPaneKeys(context.Background(), "p1", []string{"ctrl+v", "alt+v", "enter", "esc"}); err != nil {
		t.Fatalf("SendPaneKeys() error = %v", err)
	}
	want := []string{"pane", "send-keys", "p1", "--", "ctrl+v", "alt+v", "enter", "esc"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestSendPaneKeysWithoutKeysIsNoop(t *testing.T) {
	runner := &fakeRunner{}
	if err := New(runner).SendPaneKeys(context.Background(), "p1", nil); err != nil {
		t.Fatalf("SendPaneKeys() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v, want no herdr invocation", runner.calls)
	}
}

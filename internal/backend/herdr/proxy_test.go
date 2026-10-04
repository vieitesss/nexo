package herdr

import (
	"context"
	"errors"
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
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w-other","label":"machine/container","tokens":{"nexo-proxy":"machine/other"}},{"workspace_id":"w1","label":"renamed","tokens":{"nexo-proxy":"machine"}}]}}`),
	}}
	if err := New(runner).Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 3 || runner.calls[2].name != "herdr" || strings.Join(runner.calls[2].args, " ") != "workspace focus w1" {
		t.Fatalf("calls = %#v, want list, remote switch, then focus", runner.calls)
	}
}

func TestProxyReusesTaggedMachineWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine","tokens":{"nexo-proxy":"machine"}}]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"machine"}}, "")
	if err := backend.Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 3 || strings.Join(runner.calls[2].args, " ") != "workspace focus w1" {
		t.Fatalf("calls = %#v, want remote switch then focus", runner.calls)
	}
}

func TestProxyReusesTaggedBackendWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine","tokens":{"nexo-proxy":"machine"}}]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Backends: map[string]string{" MACHINE ": "herdr"}}, "")
	if err := backend.Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 3 || strings.Join(runner.calls[2].args, " ") != "workspace focus w1" {
		t.Fatalf("calls = %#v, want remote switch then focus", runner.calls)
	}
}

type herdrProxyRestartRunner struct {
	calls []call
	lists int
}

func (r *herdrProxyRestartRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
	key := strings.Join(args, " ")
	switch {
	case key == "workspace list":
		r.lists++
		if r.lists == 1 {
			return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine","tokens":{"nexo-proxy":"machine"}}]}}`), nil
		}
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w2","label":"machine","tokens":{"nexo-proxy":"machine"}}]}}`), nil
	case name == "ssh":
		return nil, errors.New("proxy pid is stale")
	case key == "workspace close w1":
		return nil, nil
	case strings.HasPrefix(key, "workspace create "):
		return []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w2"},"root_pane":{"pane_id":"p2"}}}`), nil
	default:
		return nil, nil
	}
}

func TestProxyFallbackClosesStaleWorkspaceAndCreatesInitialAttach(t *testing.T) {
	runner := &herdrProxyRestartRunner{}
	backend := New(runner)
	backend.usageStateDir = t.TempDir()
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"machine"}}, "")
	if err := backend.Proxy(context.Background(), "machine", "target"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	closeIndex, createIndex, sendIndex := -1, -1, -1
	for i, current := range runner.calls {
		key := strings.Join(current.args, " ")
		switch {
		case key == "workspace close w1":
			closeIndex = i
		case strings.HasPrefix(key, "workspace create "):
			createIndex = i
		case len(current.args) >= 2 && current.args[0] == "pane" && current.args[1] == "send-text":
			sendIndex = i
			if !strings.Contains(current.args[3], "remote attach --initial 'target' --from-env") {
				t.Fatalf("restart command = %q, want initial target", current.args[3])
			}
		}
	}
	if closeIndex < 0 || createIndex <= closeIndex || sendIndex <= createIndex {
		t.Fatalf("calls = %#v, want close stale workspace then create and send fresh pane command", runner.calls)
	}
	for _, current := range runner.calls {
		if current.name == "herdr" && len(current.args) > 1 && current.args[0] == "pane" && current.args[1] == "send-keys" {
			t.Fatalf("fallback sent keys to stale pane: %#v", runner.calls)
		}
	}
}

func TestProxyStoresTokenAndStartsSharedAttachCommand(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[]}}`),
		"workspace create --label machine --focus": []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"p1"}}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{}, "/tmp/nexo config.toml")
	if err := backend.Proxy(context.Background(), "machine", "container"); err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if len(runner.calls) != 6 {
		t.Fatalf("calls = %#v, want list, create, metadata, verification, pane input, and focus", runner.calls)
	}
	if got := strings.Join(runner.calls[2].args, " "); got != "workspace report-metadata w1 --source nexo --token nexo-proxy=machine" {
		t.Fatalf("metadata call = %q", got)
	}
	if runner.calls[4].args[0] != "pane" || runner.calls[4].args[1] != "send-text" || runner.calls[4].args[2] != "p1" || !strings.Contains(runner.calls[4].args[3], "remote attach --initial 'container' --from-env") || !strings.Contains(runner.calls[4].args[3], "exec ") {
		t.Fatalf("pane call = %#v, want shared attach command", runner.calls[4])
	}
}

func TestProxySessionIDDoesNotFallbackFromMissingLegacyIdentity(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"machine","tokens":{"nexo-proxy":"machine"}}]}}`),
	}}
	backend := New(runner)
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"machine"}}, "")
	id, err := backend.ProxySessionID(context.Background(), "machine", "container")
	if err != nil {
		t.Fatalf("ProxySessionID() error = %v", err)
	}
	if id != "" {
		t.Fatalf("ProxySessionID() = %q, want no machine-only fallback for explicit container", id)
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
			"workspace create --label machine --focus": []byte(`{"result":{"type":"workspace_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"p1"}}}`)},
		errors: map[string]error{
			"workspace report-metadata w1 --source nexo --token nexo-proxy=machine": context.DeadlineExceeded,
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

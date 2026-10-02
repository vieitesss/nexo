package herdr

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/containerstate"
)

func TestRecordFocusEventRecordsLocalWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"},{"workspace_id":"w2","label":"other"}]}}`),
	}}
	backend := New(runner)
	backend.usageStateDir = t.TempDir()

	if err := backend.RecordFocusEvent(context.Background(), []byte(`{"type":"workspace_focused","workspace_id":"w1"}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "w1" || entries[0].Proxy || entries[0].Machine != localMachine() {
		t.Fatalf("entries = %#v, want the local workspace w1", entries)
	}
}

func TestRecordFocusEventClassifiesProxyWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"proxy","label":"devbox/api","tokens":{"nexo-proxy":"devbox/api"}}]}}`),
	}}
	backend := New(runner)
	backend.usageStateDir = t.TempDir()
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"devbox"}}, "")

	if err := backend.RecordFocusEvent(context.Background(), []byte(`{"type":"pane_focused","pane_id":"p1","workspace_id":"proxy"}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Proxy || entries[0].Machine != "devbox" || entries[0].Name != "api" {
		t.Fatalf("entries = %#v, want the devbox/api proxy identity", entries)
	}
}

func TestRecordFocusEventIgnoresPayloadWithoutWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"}]}}`),
	}}
	backend := New(runner)
	backend.usageStateDir = t.TempDir()

	if err := backend.RecordFocusEvent(context.Background(), []byte(`{"type":"pane_exited","pane_id":"p1","workspace_id":"w1"}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v, want no history for an unrelated event", entries)
	}
}

func TestOpenContainerRecordsProxyWorkspace(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"proxy","label":"devbox/api","tokens":{"nexo-proxy":"devbox/api"}}]}}`),
	}}
	backend := New(runner)
	backend.usageStateDir = t.TempDir()
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"devbox"}}, "")

	if err := backend.OpenContainer(context.Background(), "proxy"); err != nil {
		t.Fatal(err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Proxy || entries[0].Machine != "devbox" || entries[0].Name != "api" {
		t.Fatalf("entries = %#v, want the proxy identity rather than a local one", entries)
	}
}

func TestHerdrProxyHistoryDeduplicatesRecreatedNativeID(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"workspace list": []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w2","label":"devbox/api","tokens":{"nexo-proxy":"devbox/api"}}]}}`),
	}}
	backend := New(runner)
	backend.usageStateDir = t.TempDir()
	backend.ConfigureRemoteConfig(config.RemoteConfig{Machines: []string{"devbox"}}, "")

	// A reconnect or workspace recreation changes the native id but not the
	// canonical machine/container identity.
	if err := backend.recordContainer(containerstate.Entry{ID: "w1", Name: "api", Machine: "devbox", Proxy: true}); err != nil {
		t.Fatal(err)
	}
	if err := backend.RecordFocusEvent(context.Background(), []byte(`{"type":"workspace_focused","workspace_id":"w2"}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := backend.RecentContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "w2" || !entries[0].Proxy {
		t.Fatalf("entries = %#v, want one proxy identity with the recreated id", entries)
	}
}

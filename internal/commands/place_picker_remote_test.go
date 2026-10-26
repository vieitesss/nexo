package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
)

func TestConfiguredRemoteContainerResolverReturnsClearUnsupportedErrors(t *testing.T) {
	resolver := containerActionResolver(Invocation{Config: config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}}})
	actions, err := resolver.ResolveContainerActions(context.Background(), "devbox")
	if err != nil {
		t.Fatalf("ResolveContainerActions() error = %v, want resolver", err)
	}
	if err := actions.OpenContainer(context.Background(), "c1"); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("OpenContainer() error = %v, want clear unsupported error", err)
	}
	if err := actions.RenameContainer(context.Background(), "c1", "new"); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("RenameContainer() error = %v, want clear unsupported error", err)
	}
	if err := actions.KillContainer(context.Background(), "c1"); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("KillContainer() error = %v, want clear unsupported error", err)
	}
}

// TestRemotePickerOpensAHerdrWorkspaceByID pins the picker target for a Herdr
// remote: the label is display data that another workspace may use as its ID,
// and two workspaces may share a label, so only the selected row's ID can name
// it unambiguously.
func TestRemotePickerOpensAHerdrWorkspaceByID(t *testing.T) {
	proxies := &recordingRemoteProxy{}
	commands := remotePlaceCommands(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"gpu1"}}},
		Adapter:   proxies,
		Runner: herdrWorkspaceListing(
			`{"workspace_id":"w1","label":"w2"}`,
			`{"workspace_id":"w2","label":"api"}`,
			`{"workspace_id":"w3","label":"api"}`,
		),
	})
	batch := commands[0]().(picker.RemoteBatch)
	if batch.Error != "" {
		t.Fatalf("remote batch error = %q", batch.Error)
	}
	rows := map[string]picker.Place{}
	for _, place := range batch.Places {
		rows[place.ContainerID] = place
	}
	for _, test := range []struct{ id, label string }{
		{id: "w1", label: "w2"},
		{id: "w2", label: "api"},
		{id: "w3", label: "api"},
	} {
		place, ok := rows[test.id]
		if !ok || place.ContainerName != test.label {
			t.Fatalf("rows = %#v, want a row for %s labelled %s", batch.Places, test.id, test.label)
		}
		if err := place.OpenAction(context.Background(), place); err != nil {
			t.Fatalf("open %s labelled %s: error = %v", test.id, test.label, err)
		}
		if proxies.kind != "herdr" || proxies.opened != [2]string{"gpu1", test.id} {
			t.Fatalf("open %s labelled %s: kind = %q target = %#v, want the Herdr workspace ID", test.id, test.label, proxies.kind, proxies.opened)
		}
	}
}

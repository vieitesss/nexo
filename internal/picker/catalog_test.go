package picker

import (
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestCatalogSnapshotMergesSourcesAndAssociatesAgents(t *testing.T) {
	catalog := NewCatalog(nil, []capabilities.Container{
		{ID: "proxy", Name: "api", Machine: "DEV", Proxy: true},
		{ID: "other", Name: "local-api"},
	}, []capabilities.Agent{
		{PaneID: "local", ContainerID: "other", Container: "api", State: "working"},
	})
	catalog.SetRemoteMachines([]string{"Dev"})
	catalog.ApplyRemote(RemoteBatch{
		Machine: "dev",
		Backend: "tmux",
		Places:  []Place{{Kind: ContainerKind, ContainerID: "remote", ContainerName: "api", Machine: "DEV"}},
		Agents:  []capabilities.Agent{{PaneID: "remote-agent", ContainerID: "remote", Container: "api", State: "stopped"}},
	})

	snapshot := catalog.Snapshot()
	if len(snapshot.Places) != 2 {
		t.Fatalf("places = %#v, want merged api and local rows", snapshot.Places)
	}
	var merged, local Place
	for _, place := range snapshot.Places {
		if place.Machine == "dev" {
			merged = place
		}
		if place.ContainerID == "other" {
			local = place
		}
	}
	if merged.ContainerID != "proxy" || !merged.Proxy || merged.OpenAction != nil {
		t.Fatalf("merged proxy = %#v, want local identity and proxy", merged)
	}
	if len(merged.Agents) != 1 || merged.Agents[0].PaneID != "remote-agent" {
		t.Fatalf("remote agents = %#v, want one associated agent", merged.Agents)
	}
	if len(local.Agents) != 1 || local.Agents[0].PaneID != "local" {
		t.Fatalf("local agents = %#v, want one associated agent", local.Agents)
	}

	catalog.ApplyRemote(RemoteBatch{Machine: "DEV", Error: "offline"})
	snapshot = catalog.Snapshot()
	for _, place := range snapshot.Places {
		if place.Machine == "dev" && (!place.Proxy || place.ContainerID != "proxy") {
			t.Fatalf("failed remote refresh removed proxy identity: %#v", place)
		}
	}
	if got := snapshot.Status["dev"]; !got.Loaded || got.Error != "offline" {
		t.Fatalf("status = %#v, want loaded offline status", got)
	}
}

func TestCatalogReplacesRemoteAgentsAndMarkedUsesNormalizedIdentity(t *testing.T) {
	catalog := NewCatalog(nil, []capabilities.Container{{ID: "proxy", Name: "api", Machine: "remote", Proxy: true}}, nil)
	catalog.ApplyRemote(RemoteBatch{Machine: "REMOTE", Agents: []capabilities.Agent{{PaneID: "old", State: "working"}}})
	catalog.ApplyRemote(RemoteBatch{Machine: "remote", Agents: []capabilities.Agent{{PaneID: "new", State: "stopped"}}})
	snapshot := catalog.Snapshot()
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].Agent.PaneID != "new" {
		t.Fatalf("agents = %#v, want replaced remote snapshot", snapshot.Agents)
	}
	marked := catalog.Marked(map[ContainerRef]bool{{Machine: "REMOTE", ID: "proxy"}: true})
	if len(marked) != 1 || marked[0] != (ContainerRef{Machine: "remote", ID: "proxy"}) {
		t.Fatalf("marked = %#v, want normalized remote identity", marked)
	}
}

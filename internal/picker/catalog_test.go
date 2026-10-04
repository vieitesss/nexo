package picker

import (
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

func TestCatalogSnapshotKeepsProxySeparateFromSameNamedRemote(t *testing.T) {
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
	if len(snapshot.Places) != 3 {
		t.Fatalf("places = %#v, want separate proxy, remote, and local rows", snapshot.Places)
	}
	var proxy, remote, local Place
	for _, place := range snapshot.Places {
		if place.ContainerID == "proxy" {
			proxy = place
		}
		if place.ContainerID == "remote" {
			remote = place
		}
		if place.ContainerID == "other" {
			local = place
		}
	}
	if !proxy.Proxy || proxy.RemoteContainerID != "" || len(proxy.Agents) != 0 {
		t.Fatalf("proxy = %#v, want isolated proxy identity", proxy)
	}
	if len(remote.Agents) != 1 || remote.Agents[0].PaneID != "remote-agent" {
		t.Fatalf("remote agents = %#v, want one associated agent", remote.Agents)
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

func TestCatalogKeepsDistinctContainersWithSharedLabel(t *testing.T) {
	catalog := NewCatalog(nil, []capabilities.Container{
		{ID: "one", Name: "api"},
		{ID: "two", Name: "api"},
	}, []capabilities.Agent{
		{PaneID: "first", ContainerID: "one", Container: "api", State: "working"},
		{PaneID: "second", ContainerID: "two", Container: "api", State: "stopped"},
		{PaneID: "ambiguous", Container: "api", State: "working"},
	})
	snapshot := catalog.Snapshot()
	if len(snapshot.Places) != 2 {
		t.Fatalf("places = %#v, want both same-label containers", snapshot.Places)
	}
	byID := map[string]Place{}
	for _, place := range snapshot.Places {
		byID[place.ContainerID] = place
	}
	for id, want := range map[string]string{"one": "first", "two": "second"} {
		place, ok := byID[id]
		if !ok {
			t.Fatalf("container %q dropped from snapshot: %#v", id, snapshot.Places)
		}
		if len(place.Agents) != 1 || place.Agents[0].PaneID != want {
			t.Fatalf("container %q agents = %#v, want only %q", id, place.Agents, want)
		}
	}
	local := containerstate.LocalMachine()
	marked := catalog.Marked(map[ContainerRef]bool{
		{Machine: local, ID: "one"}: true,
		{Machine: local, ID: "two"}: true,
	})
	if len(marked) != 2 || marked[0] != (ContainerRef{Machine: local, ID: "one"}) || marked[1] != (ContainerRef{Machine: local, ID: "two"}) {
		t.Fatalf("marked = %#v, want both local identities", marked)
	}
}

func TestCatalogNeverReassignsIDBearingAgentByLabel(t *testing.T) {
	catalog := NewCatalog(nil, []capabilities.Container{{ID: "new", Name: "api"}}, []capabilities.Agent{
		{PaneID: "stale", ContainerID: "old", Container: "api", State: "stopped"},
	})
	snapshot := catalog.Snapshot()
	if len(snapshot.Places) != 1 {
		t.Fatalf("places = %#v", snapshot.Places)
	}
	if len(snapshot.Places[0].Agents) != 0 {
		t.Fatalf("stale same-label agent attached by label: %#v", snapshot.Places[0].Agents)
	}
	catalog.ReplaceLocalAgents([]capabilities.Agent{
		{PaneID: "fresh", ContainerID: "new", Container: "api", State: "working"},
	})
	snapshot = catalog.Snapshot()
	if len(snapshot.Places[0].Agents) != 1 || snapshot.Places[0].Agents[0].PaneID != "fresh" {
		t.Fatalf("authoritative ID did not associate: %#v", snapshot.Places[0].Agents)
	}
}

func TestCatalogKeepsProxySeparateFromRemoteAgentAssociation(t *testing.T) {
	catalog := NewCatalog(nil, []capabilities.Container{{ID: "proxy", Name: "api", Machine: "dev", Proxy: true}}, nil)
	catalog.ApplyRemote(RemoteBatch{
		Machine: "dev",
		Places:  []Place{{Kind: ContainerKind, ContainerID: "new", ContainerName: "api"}},
		Agents:  []capabilities.Agent{{PaneID: "stale", ContainerID: "old", Container: "api", State: "working"}},
	})
	snapshot := catalog.Snapshot()
	if len(snapshot.Places) != 2 {
		t.Fatalf("places = %#v, want separate proxy and remote rows", snapshot.Places)
	}
	proxy := snapshot.Places[0]
	if proxy.ContainerID != "proxy" || proxy.RemoteContainerID != "" || len(proxy.Agents) != 0 {
		t.Fatalf("proxy identity = %#v, want isolated local identity", proxy)
	}
	catalog.ApplyRemote(RemoteBatch{
		Machine: "dev",
		Places:  []Place{{Kind: ContainerKind, ContainerID: "new", ContainerName: "api"}},
		Agents:  []capabilities.Agent{{PaneID: "remote-agent", ContainerID: "new", Container: "api", State: "working"}},
	})
	snapshot = catalog.Snapshot()
	if len(snapshot.Places) != 2 {
		t.Fatalf("places = %#v, want separate proxy and remote rows", snapshot.Places)
	}
	if len(snapshot.Places[0].Agents) != 0 || len(snapshot.Places[1].Agents) != 1 || snapshot.Places[1].Agents[0].PaneID != "remote-agent" {
		t.Fatalf("remote agent association = %#v, want remote row only", snapshot.Places)
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

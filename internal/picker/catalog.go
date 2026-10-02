package picker

import (
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
)

// CatalogAgent is one agent snapshot with the machine namespace supplied by
// its source. Agent records intentionally keep the backend as decoration for
// callers that need to focus an agent; association itself uses only Machine.
type CatalogAgent struct {
	Agent   capabilities.Agent
	Machine string
	Backend string
}

// MachineStatus describes the latest asynchronous source state for a machine.
type MachineStatus struct {
	Loaded  bool
	Warning string
	Error   string
	// Backend is the multiplexer kind a successful remote listing resolved.
	// It lets a row carry the resolved kind so an action never probes again.
	Backend string
}

// CatalogSnapshot is the coherent read interface of a Catalog. Places are
// already proxy-merged, machine-normalized, and decorated with their latest
// agent state. Agents and machine status are returned from the same update
// sequence, so a caller never has to reconstruct source ownership.
type CatalogSnapshot struct {
	Places   []Place
	Agents   []CatalogAgent
	Machines []string
	Status   map[string]MachineStatus
}

// Catalog is the deep module for place snapshots. It owns machine identity,
// source replacement, proxy merging, and agent association behind Snapshot.
// The TUI and scripted listings should consume that snapshot rather than
// repeating those policies at their seams.
type Catalog struct {
	projects        []Place
	localContainers []Place
	localAgents     []capabilities.Agent
	remote          map[string]remoteSnapshot
	loading         map[string]bool
	machines        []string
}

type remoteSnapshot struct {
	backend string
	places  []Place
	agents  []capabilities.Agent
	status  MachineStatus
}

// NewCatalog creates a catalog from the initial local snapshots. Inputs are
// copied; subsequent updates replace one source snapshot rather than layering
// stale rows over it.
func NewCatalog(projects []Place, containers []capabilities.Container, agents []capabilities.Agent) *Catalog {
	catalog := &Catalog{remote: make(map[string]remoteSnapshot)}
	catalog.SetProjects(projects)
	catalog.ReplaceContainers(containers)
	catalog.ReplaceLocalAgents(agents)
	return catalog
}

// SetProjects replaces project candidates while preserving their discovery
// order. Project updates are kept separate from machine-owned containers.
func (c *Catalog) SetProjects(projects []Place) {
	c.projects = append([]Place(nil), projects...)
}

// ReplaceContainers replaces the local container source. A replacement is
// deliberately atomic from the snapshot interface, preventing stale rows
// from surviving a refresh.
func (c *Catalog) ReplaceContainers(containers []capabilities.Container) {
	c.localContainers = ContainerPlaces(containers)
	c.normalizePlaces(c.localContainers)
}

// ReplaceLocalAgents replaces the local agent source as one machine snapshot.
func (c *Catalog) ReplaceLocalAgents(agents []capabilities.Agent) {
	c.localAgents = append([]capabilities.Agent(nil), agents...)
}

// SetRemoteMachines records configured sources before their asynchronous
// batches arrive. This makes loading and empty-result states visible through
// the same snapshot interface as populated machines.
func (c *Catalog) SetRemoteMachines(machines []string) {
	for _, machine := range machines {
		machine = NormalizeMachine(machine)
		if machine == "" || machine == containerstate.LocalMachine() || c.hasMachine(machine) {
			continue
		}
		c.machines = append(c.machines, machine)
	}
}

// MarkRemoteLoading flags the given machines as awaiting a fresh listing.
// Their last containers stay visible, but the snapshot reports them unloaded
// with no backend, warning or error, so a coordinated reload hides action rows
// and stale errors until ApplyRemote accepts the current result. Marking the
// catalog itself keeps the policy independent of cached status that a local
// refresh would otherwise restore.
func (c *Catalog) MarkRemoteLoading(machines []string) {
	for _, machine := range machines {
		machine = NormalizeMachine(machine)
		if machine == "" || machine == containerstate.LocalMachine() {
			continue
		}
		if c.loading == nil {
			c.loading = make(map[string]bool)
		}
		c.loading[machine] = true
	}
}

// ApplyRemote replaces one remote machine's places and agents. A failed or
// empty batch removes stale remote rows while local proxies remain available
// to MergeContainerPlaces. The machine value is the stable namespace; display
// labels never participate in source replacement.
func (c *Catalog) ApplyRemote(batch RemoteBatch) {
	machine := NormalizeMachine(batch.Machine)
	if machine == "" || machine == containerstate.LocalMachine() {
		return
	}
	delete(c.loading, machine)
	if c.remote == nil {
		c.remote = make(map[string]remoteSnapshot)
	}
	if !c.hasMachine(machine) {
		c.machines = append(c.machines, machine)
	}
	places := append([]Place(nil), batch.Places...)
	for index := range places {
		places[index].Kind = ContainerKind
		places[index].Machine = machine
		places[index].Group = machine
	}
	c.normalizePlaces(places)
	c.remote[machine] = remoteSnapshot{
		backend: batch.Backend,
		places:  places,
		agents:  append([]capabilities.Agent(nil), batch.Agents...),
		status: MachineStatus{
			Loaded:  true,
			Warning: strings.TrimSpace(batch.Warning),
			Error:   strings.TrimSpace(batch.Error),
		},
	}
}

// Snapshot materializes one coherent catalog view. It is safe for callers to
// retain and mutate the returned slices; the catalog keeps its own state.
func (c *Catalog) Snapshot() CatalogSnapshot {
	if c == nil {
		return CatalogSnapshot{Status: map[string]MachineStatus{}}
	}
	places := append([]Place(nil), c.projects...)
	local := append([]Place(nil), c.localContainers...)
	c.normalizePlaces(local)
	places = append(places, local...)
	// Keep the full local snapshot as the merge base. Distinct local rows may
	// share a machine and label, so they must never travel through the merge's
	// extra argument; each remote snapshot then merges into that base in turn.
	for _, machine := range c.remoteOrderForSnapshot() {
		if source, ok := c.remote[machine]; ok {
			places = MergeContainerPlaces(places, source.places)
		}
	}
	c.normalizePlaces(places)

	agents := make([]CatalogAgent, 0, len(c.localAgents))
	localMachine := containerstate.LocalMachine()
	for _, agent := range c.localAgents {
		agents = append(agents, CatalogAgent{Agent: agent, Machine: localMachine})
	}
	for _, machine := range c.remoteOrderForSnapshot() {
		source, ok := c.remote[machine]
		if !ok {
			continue
		}
		for _, agent := range source.agents {
			agents = append(agents, CatalogAgent{Agent: agent, Machine: machine, Backend: source.backend})
		}
	}

	// Association is performed once over the complete place snapshot. This is
	// the seam that keeps authoritative-ID matching and the unique-label
	// fallback scoped to each machine, instead of making every row ask the
	// helper independently.
	for _, machine := range c.snapshotMachines(places, agents) {
		var source []capabilities.Agent
		if machine == localMachine {
			source = c.localAgents
		} else if remote, ok := c.remote[machine]; ok {
			source = remote.agents
		}
		places = associateAgents(places, source, machine)
	}

	status := make(map[string]MachineStatus, len(c.remote)+1)
	status[localMachine] = MachineStatus{Loaded: true}
	for machine, source := range c.remote {
		resolved := MachineStatus{
			Loaded:  source.status.Loaded,
			Warning: source.status.Warning,
			Error:   source.status.Error,
			Backend: source.backend,
		}
		if c.loading[machine] {
			resolved = MachineStatus{}
		}
		status[machine] = resolved
	}
	machines := c.snapshotMachines(places, agents)
	return CatalogSnapshot{
		Places:   places,
		Agents:   agents,
		Machines: machines,
		Status:   status,
	}
}

// Marked returns marked container identities in source order from this
// snapshot. Machine normalization keeps marks stable across remote spellings
// and refreshes. Deriving marks from an already-materialized snapshot lets a
// caller reuse one coherent view for both counts and marks.
func (s CatalogSnapshot) Marked(marked map[ContainerRef]bool) []ContainerRef {
	normalizedMarked := make(map[ContainerRef]bool, len(marked))
	for ref, value := range marked {
		normalizedMarked[ContainerRef{Machine: NormalizeMachine(ref.Machine), ID: ref.ID}] = value
	}
	refs := make([]ContainerRef, 0)
	seen := make(map[ContainerRef]bool)
	for _, place := range s.Places {
		ref := ContainerRef{Machine: NormalizeMachine(place.Machine), ID: place.ContainerID}
		if place.Kind != ContainerKind || ref.ID == "" || !normalizedMarked[ref] || seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// Marked returns marked container identities in source order from the current
// coherent snapshot.
func (c *Catalog) Marked(marked map[ContainerRef]bool) []ContainerRef {
	return c.Snapshot().Marked(marked)
}

// NormalizeMachine returns the stable machine namespace used by all catalog
// source matching. The local name remains the actual hostname for identity
// compatibility, while remote names are case-insensitive.
func NormalizeMachine(machine string) string {
	machine = strings.TrimSpace(machine)
	if machine == "" || strings.EqualFold(machine, "local") || strings.EqualFold(machine, containerstate.LocalMachine()) {
		return containerstate.LocalMachine()
	}
	return strings.ToLower(machine)
}

func (c *Catalog) normalizePlaces(places []Place) {
	for index := range places {
		if places[index].Kind != ContainerKind {
			continue
		}
		machine := NormalizeMachine(places[index].Machine)
		places[index].Machine = machine
		places[index].Group = machine
	}
}

func (c *Catalog) hasMachine(machine string) bool {
	for _, existing := range c.machines {
		if existing == machine {
			return true
		}
	}
	return false
}

func (c *Catalog) remoteOrderForSnapshot() []string {
	order := make([]string, 0, len(c.machines)+len(c.remote))
	for _, machine := range c.machines {
		if machine != containerstate.LocalMachine() && !containsString(order, machine) {
			order = append(order, machine)
		}
	}
	for machine := range c.remote {
		if machine != containerstate.LocalMachine() && !containsString(order, machine) {
			order = append(order, machine)
		}
	}
	return order
}

func (c *Catalog) snapshotMachines(places []Place, agents []CatalogAgent) []string {
	seen := map[string]bool{containerstate.LocalMachine(): true}
	for _, machine := range c.machines {
		if machine != "" {
			seen[machine] = true
		}
	}
	for _, place := range places {
		if place.Kind == ContainerKind {
			machine := NormalizeMachine(place.Machine)
			if machine != "" {
				seen[machine] = true
			}
		}
	}
	for _, agent := range agents {
		machine := NormalizeMachine(agent.Machine)
		if machine != "" {
			seen[machine] = true
		}
	}
	machines := make([]string, 0, len(seen))
	for machine := range seen {
		machines = append(machines, machine)
	}
	local := containerstate.LocalMachine()
	// Keep machine membership deterministic while leaving row ordering and
	// display choices to the caller's projection.
	for index := range machines {
		if machines[index] == local {
			machines[0], machines[index] = machines[index], machines[0]
			break
		}
	}
	for start := 1; start < len(machines); start++ {
		for index := start + 1; index < len(machines); index++ {
			if machines[index] < machines[start] {
				machines[start], machines[index] = machines[index], machines[start]
			}
		}
	}
	return machines
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

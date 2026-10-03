package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/containerstate"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/shell"
)

func (m Model) catalogSnapshot() picker.CatalogSnapshot {
	if m.catalog == nil {
		return picker.CatalogSnapshot{
			Places:   append([]picker.Place(nil), m.deps.Projects...),
			Machines: []string{containerstate.LocalMachine()},
			Status:   map[string]picker.MachineStatus{},
		}
	}
	return m.catalog.Snapshot()
}

// rowProjection is the internal projection module. It accepts one coherent
// place-catalog snapshot plus the small amount of view state needed for matching
// and folding, then exposes rows and counts as its narrow interface. This seam
// gives all three tabs the same identity policy: its depth is in the row
// construction, while callers get leverage from two small methods and locality
// when matching or ordering changes.
type rowProjection struct {
	snapshot       picker.CatalogSnapshot
	query          string
	width          int
	folded         map[string]bool
	expanded       map[string]bool
	agents         []agentRecord
	remoteAgents   []agentRecord
	remoteMachines []string
	remoteLoaded   map[string]bool
	remoteBackends map[string]string
	groupErrors    map[string]string
	groupWarnings  map[string]string
}

func (m Model) projection() rowProjection {
	return rowProjection{
		snapshot:       m.catalogSnapshot(),
		query:          m.input.query.Value(),
		width:          m.width,
		folded:         m.input.folded,
		expanded:       m.input.expanded,
		agents:         append([]agentRecord(nil), m.source.agents...),
		remoteAgents:   append([]agentRecord(nil), m.source.remoteAgents...),
		remoteMachines: append([]string(nil), m.source.remoteMachines...),
		remoteLoaded:   m.source.remoteLoaded,
		remoteBackends: m.source.remoteBackends,
		groupErrors:    m.source.groupErrors,
		groupWarnings:  m.source.groupWarnings,
	}
}

func gitIdentity(place picker.Place) string {
	if place.GitContext == nil {
		return ""
	}
	return place.GitContext.Identity
}

func containerPlacesFrom(places []picker.Place) []picker.Place {
	out := make([]picker.Place, 0, len(places))
	for _, place := range places {
		if place.Kind == picker.ContainerKind {
			out = append(out, place)
		}
	}
	return out
}

func attentionRank(agent capabilities.Agent) int { return picker.AttentionRank(agent) }

func agentIdentity(record agentRecord) string {
	identity := picker.AgentIdentity(record.agent)
	return "agent:" + record.machine + ":" + identity
}

func hasUnseenStopped(agents []capabilities.Agent) bool {
	for _, agent := range agents {
		if picker.AttentionRank(agent) == 0 {
			return true
		}
	}
	return false
}

func shortenMachine(machine string) string {
	machine = sanitize(machine)
	if index := strings.IndexByte(machine, '.'); index >= 0 {
		machine = machine[:index]
	}
	return strings.TrimSuffix(machine, ".local")
}

func containerIdentity(place picker.Place) string {
	return "container:" + place.Machine + ":" + place.ContainerID
}

// projectMatchBefore orders ranked search results. Scores already encode
// field precedence (name, then branch, then path). Equal scores fall back to
// a stable reading order: the shorter name first, a checkout before its
// worktrees, then the name itself.
func projectMatchBefore(left, right row) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	leftName, rightName := []rune(left.label), []rune(right.label)
	if len(leftName) != len(rightName) {
		return len(leftName) < len(rightName)
	}
	leftRoot, rightRoot := left.place.ParentPath == "", right.place.ParentPath == ""
	if leftRoot != rightRoot {
		return leftRoot
	}
	return left.label < right.label
}

func containsPlacePath(places []picker.Place, path string) bool {
	for _, place := range places {
		if place.Path == path {
			return true
		}
	}
	return false
}

func projectIdentity(place picker.Place) string { return "project:" + place.Path }

func projectLabel(place picker.Place) string {
	if place.DisplayRemainder != "" {
		return sanitize(place.DisplayRemainder)
	}
	return sanitize(place.Path)
}

func abbreviateHome(value string) string {
	value = sanitize(value)
	home := sanitize(os.Getenv("HOME"))
	if home == "" {
		if detected, err := os.UserHomeDir(); err == nil {
			home = sanitize(detected)
		}
	}
	if home == "" {
		return value
	}
	home = strings.TrimRight(filepath.ToSlash(home), "/")
	value = filepath.ToSlash(value)
	if value == home {
		return "~"
	}
	if strings.HasPrefix(value, home+"/") {
		return "~" + strings.TrimPrefix(value, home)
	}
	return value
}

func newAgentRecords(agents []capabilities.Agent, machine string) []agentRecord {
	if machine == "" {
		machine = containerstate.LocalMachine()
	}
	result := make([]agentRecord, 0, len(agents))
	for _, agent := range agents {
		result = append(result, agentRecord{agent: agent, machine: machine})
	}
	return result
}

func (p rowProjection) projectPlaces() []picker.Place {
	places := append([]picker.Place(nil), p.snapshot.Places...)
	out := places[:0]
	for _, place := range places {
		if place.Kind == picker.ProjectKind {
			out = append(out, place)
		}
	}
	return out
}

func (p rowProjection) matchAgent(record agentRecord) (picker.Match, bool) {
	agent := record.agent
	return picker.Rank(string(p.query), strings.Join([]string{sanitize(agent.Harness), sanitize(agent.Container), sanitize(record.machine), sanitize(agent.Target), sanitize(agent.Branch)}, " "))
}

func (p rowProjection) matchContainer(place picker.Place) (picker.Match, bool) {
	text := strings.Join([]string{sanitize(place.ContainerName), sanitize(place.Machine)}, " ")
	return picker.Rank(string(p.query), text)
}

func (p rowProjection) matchProject(place picker.Place) (picker.Match, bool) {
	query := string(p.query)
	if query == "" {
		return picker.Match{}, true
	}

	// A project name is the strongest signal.  Ranking one combined string
	// lets a long checkout path beat an exact project name, which is especially
	// surprising for short queries.  Keep the fields separate and make that
	// precedence explicit instead.
	name := projectLabel(place)
	if match, ok := picker.Rank(query, name); ok {
		match.Score += 100000
		return match, true
	}
	if branch := gitIdentity(place); branch != "" {
		if match, ok := picker.Rank(query, sanitize(branch)); ok {
			match.Score += 50000
			return match, true
		}
	}
	path := sanitize(place.SearchText)
	if path == "" {
		path = sanitize(place.Path)
	}
	if match, ok := picker.Rank(query, path); ok {
		// A sparse match spanning a full path is noise.  Contiguous path
		// matches remain useful when the project name itself is unavailable.
		if match.Score < len([]rune(query))*10 {
			return picker.Match{}, false
		}
		return match, true
	}
	return picker.Match{}, false
}

func (p rowProjection) MatchCount(tab Tab) int {
	switch tab {
	case AgentsTab:
		n := 0
		for _, agent := range p.allAgents() {
			if _, ok := p.matchAgent(agent); ok {
				n++
			}
		}
		return n
	case ContainersTab:
		n := 0
		for _, place := range p.containerPlaces() {
			if _, ok := p.matchContainer(place); ok {
				n++
			}
		}
		return n
	default:
		n := 0
		for _, place := range p.projectPlaces() {
			if _, ok := p.matchProject(place); ok {
				n++
			}
		}
		return n
	}
}

func (p rowProjection) containerPlaces() []picker.Place {
	return containerPlacesFrom(p.snapshot.Places)
}

// markedTargets derives marked identities from the snapshot the projection
// already holds, so frame construction or a mouse hit test asks the catalog
// for one coherent view instead of materializing a second one for the marks.
func (p rowProjection) markedTargets(marked map[picker.ContainerRef]bool) []picker.ContainerRef {
	return p.snapshot.Marked(marked)
}

func (p rowProjection) Rows(tab Tab) []row {
	switch tab {
	case AgentsTab:
		return p.agentRows()
	case ContainersTab:
		return p.containerRows()
	default:
		return p.projectRows()
	}
}

func (p rowProjection) agentRows() []row {
	rows := make([]row, 0)
	machines := p.machineNames()
	for _, machine := range machines {
		children := make([]row, 0)
		for _, record := range p.allAgents() {
			if record.machine != machine {
				continue
			}
			match, ok := p.matchAgent(record)
			if !ok {
				continue
			}
			agent := record.agent
			children = append(children, row{id: agentIdentity(record), kind: itemRow, itemKind: "agent", label: agent.Harness, machine: machine, group: machine, depth: 2, cursorable: true, matched: true, score: match.Score, agent: agent, agentMachine: record.machine, agentBackend: record.backend})
		}
		sort.SliceStable(children, func(i, j int) bool {
			ri, rj := attentionRank(children[i].agent), attentionRank(children[j].agent)
			if ri != rj {
				return ri < rj
			}
			if ri == 0 && children[i].agent.StoppedSince != nil && children[j].agent.StoppedSince != nil && !children[i].agent.StoppedSince.Equal(*children[j].agent.StoppedSince) {
				return children[i].agent.StoppedSince.Before(*children[j].agent.StoppedSince)
			}
			if string(p.query) != "" && children[i].score != children[j].score {
				return children[i].score > children[j].score
			}
			return children[i].id < children[j].id
		})
		remoteErr := p.groupErrors[machine]
		remoteWarn := p.groupWarnings[machine]
		isRemote := contains(p.remoteMachines, machine)
		_, machineMatch := picker.Rank(string(p.query), sanitize(machine))
		if len(children) == 0 && !machineMatch && remoteErr == "" && remoteWarn == "" && !isRemote && string(p.query) != "" {
			continue
		}
		rows = append(rows, row{id: "machine:" + machine, kind: machineRow, label: shortenMachine(machine), group: machine, machine: machine, count: len(children), remoteErr: remoteErr, remoteWarn: remoteWarn, loading: isRemote && !p.remoteLoaded[machine]})
		rows = append(rows, children...)
		if machine != machines[len(machines)-1] {
			rows = append(rows, row{kind: separatorRow})
		}
	}
	return rows
}

func (p rowProjection) machineNames() []string {
	return append([]string(nil), p.snapshot.Machines...)
}

func (p rowProjection) containerRows() []row {
	rows := []row{}
	snapshot := p.snapshot
	machines := append([]string(nil), snapshot.Machines...)
	places := containerPlacesFrom(snapshot.Places)
	if string(p.query) != "" {
		sort.SliceStable(machines, func(i, j int) bool {
			left, right := p.machineMatchScore(places, machines[i]), p.machineMatchScore(places, machines[j])
			if left != right {
				return left > right
			}
			return machines[i] < machines[j]
		})
	}
	for machineIndex, machine := range machines {
		children := []row{}
		for _, place := range places {
			if place.Machine != machine {
				continue
			}
			match, ok := p.matchContainer(place)
			if !ok {
				continue
			}
			children = append(children, row{id: containerIdentity(place), kind: itemRow, itemKind: "container", label: place.ContainerName, machine: machine, group: machine, depth: 2, cursorable: true, matched: true, score: match.Score, place: place, activity: place.Activity})
		}
		if string(p.query) != "" {
			sort.SliceStable(children, func(i, j int) bool {
				if children[i].score != children[j].score {
					return children[i].score > children[j].score
				}
				return children[i].label < children[j].label
			})
		} else {
			sort.SliceStable(children, func(i, j int) bool { return p.containerBefore(children[i], children[j]) })
		}
		nameWidth := 0
		for _, child := range children {
			if width := shell.StringWidth(sanitize(child.place.ContainerName)); width > nameWidth {
				nameWidth = width
			}
		}
		capWidth := p.width * 40 / 100
		if capWidth <= 0 {
			capWidth = 40
		}
		if nameWidth > capWidth {
			nameWidth = capWidth
		}
		for i := range children {
			children[i].nameWidth = nameWidth
		}
		_, machineMatch := picker.Rank(string(p.query), sanitize(machine))
		remoteErr := p.groupErrors[machine]
		remoteWarn := p.groupWarnings[machine]
		isRemote := contains(p.remoteMachines, machine)
		if len(children) == 0 && !machineMatch && remoteErr == "" && !isRemote && string(p.query) != "" {
			continue
		}
		rows = append(rows, row{id: "machine:" + machine, kind: machineRow, label: shortenMachine(machine), group: machine, machine: machine, count: len(children), remoteErr: remoteErr, remoteWarn: remoteWarn, loading: isRemote && !p.remoteLoaded[machine]})
		rows = append(rows, children...)
		if isRemote && p.remoteLoaded[machine] && remoteErr == "" && p.remoteBackends[machine] != "" {
			rows = append(rows, row{id: "new:" + machine, kind: newContainerRow, label: newContainerLabel, machine: machine, group: machine, depth: 2, cursorable: true, backend: p.remoteBackends[machine]})
		}
		if machineIndex < len(machines)-1 {
			rows = append(rows, row{kind: separatorRow})
		}
	}
	return rows
}

func (p rowProjection) machineMatchScore(places []picker.Place, machine string) int {
	best := -1
	for _, place := range places {
		if place.Machine != machine {
			continue
		}
		if match, ok := p.matchContainer(place); ok && match.Score > best {
			best = match.Score
		}
	}
	if match, ok := picker.Rank(string(p.query), sanitize(machine)); ok && match.Score > best {
		best = match.Score
	}
	return best
}

func (p rowProjection) containerBefore(left, right row) bool {
	leftUrgent := hasUnseenStopped(left.place.Agents)
	rightUrgent := hasUnseenStopped(right.place.Agents)
	if leftUrgent != rightUrgent {
		return leftUrgent
	}
	if left.activity == nil || right.activity == nil {
		if left.activity == nil && right.activity == nil {
			return left.label < right.label
		}
		return left.activity != nil
	}
	if !left.activity.Equal(*right.activity) {
		return left.activity.After(*right.activity)
	}
	return left.label < right.label
}

func (p rowProjection) projectRows() []row {
	places := p.projectPlaces()
	query := string(p.query)
	if query != "" {
		return p.flatProjectRows(places)
	}

	groups := make([]string, 0)
	seenGroups := map[string]bool{}
	for _, place := range places {
		if !seenGroups[place.Group] {
			seenGroups[place.Group] = true
			groups = append(groups, place.Group)
		}
	}
	rows := make([]row, 0, len(places)+len(groups))
	for _, group := range groups {
		groupPlaces := make([]picker.Place, 0)
		for _, place := range places {
			if place.Group == group {
				groupPlaces = append(groupPlaces, place)
			}
		}
		key := "group:" + group
		// Empty-query browsing is an overview of folders: every folder and every
		// checkout starts folded. Right unfolds a row explicitly.
		expanded := p.expanded[key] && !p.folded[key]
		rows = append(rows, row{id: key, kind: groupRow, label: group, group: group, count: len(groupPlaces), cursorable: true, expanded: expanded, foldable: true})
		if !expanded {
			continue
		}
		roots := make([]picker.Place, 0)
		for _, place := range groupPlaces {
			if place.ParentPath == "" || !containsPlacePath(groupPlaces, place.ParentPath) {
				roots = append(roots, place)
			}
		}
		for _, root := range roots {
			project := p.projectRow(root, group, 0, "", true, groupPlaces)
			rows = append(rows, project)
			if project.expanded {
				rows = append(rows, p.projectChildRows(root, groupPlaces, group)...)
			}
		}
	}
	return rows
}

func (p rowProjection) flatProjectRows(places []picker.Place) []row {
	rows := make([]row, 0, len(places))
	for _, place := range places {
		match, ok := p.matchProject(place)
		if !ok {
			continue
		}
		rows = append(rows, row{
			id: projectIdentity(place), kind: itemRow, itemKind: "project", label: projectLabel(place),
			group: place.Group, depth: 0, cursorable: true, matched: true, score: match.Score, place: place,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return projectMatchBefore(rows[i], rows[j]) })
	return rows
}

func (p rowProjection) projectRow(place picker.Place, group string, depth int, treePrefix string, cursorable bool, all []picker.Place) row {
	match, _ := picker.Rank("", projectLabel(place))
	children := p.children(place, all)
	expanded := len(children) == 0 || (p.expanded[projectIdentity(place)] && !p.folded[projectIdentity(place)])
	return row{id: projectIdentity(place), kind: itemRow, itemKind: "project", label: projectLabel(place), group: group, depth: depth, count: len(children), cursorable: cursorable, matched: true, score: match.Score, place: place, expanded: expanded, foldable: len(children) > 0, treePrefix: treePrefix}
}

func (p rowProjection) projectChildRows(parent picker.Place, all []picker.Place, group string) []row {
	children := p.children(parent, all)
	rows := make([]row, 0, len(children))
	for index, child := range children {
		prefix := "├ "
		if index == len(children)-1 {
			prefix = "└ "
		}
		project := p.projectRow(child, group, 1, prefix, true, all)
		rows = append(rows, project)
		if project.expanded {
			rows = append(rows, p.projectChildRows(child, all, group)...)
		}
	}
	return rows
}

func (p rowProjection) children(place picker.Place, all []picker.Place) []picker.Place {
	children := []picker.Place{}
	for _, candidate := range all {
		if candidate.ParentPath == place.Path {
			children = append(children, candidate)
		}
	}
	return children
}

func (p rowProjection) allAgents() []agentRecord {
	agents := append([]agentRecord(nil), p.agents...)
	return append(agents, p.remoteAgents...)
}

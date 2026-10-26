package rex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/agentdetect"
	"github.com/vieitesss/nexo/internal/agentstate"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/git"
)

var (
	_ capabilities.AgentSource       = (*Backend)(nil)
	_ capabilities.AgentConfigurator = (*Backend)(nil)
	_ capabilities.ScreenReader      = (*Backend)(nil)
	_ capabilities.PaneFocuser       = (*Backend)(nil)
)

// statusPriority orders the native states by the attention they need. Several
// records can share one block, and the highest-ranked recognized record names
// the block's agent; a tie keeps the earlier record, which is the root.
var statusPriority = map[string]int{"blocked": 4, "error": 3, "working": 2, "done": 1, "idle": 0}

// agentSnapshot is one block's agent evidence before the shared ledger turns it
// into durable state.
type agentSnapshot struct {
	session sessionInfo
	block   block
	harness string
	// status is the native state mapped for the ledger. It stays empty for the
	// process/screen fallback, which has no native lifecycle.
	status string
	// working is the fallback's screen observation. Native snapshots derive it
	// from status, so the native state stays the only native fact.
	working bool
	path    string
}

// ConfigureAgents applies the shared agents configuration. The recognized
// harness list and working patterns decide which native status records and
// processes count as an agent for nexo.
func (b *Backend) ConfigureAgents(cfg config.AgentsConfig) {
	if b == nil {
		return
	}
	b.agentsMu.Lock()
	defer b.agentsMu.Unlock()
	b.agents = cfg.WithDefaults()
}

// agentsConfig returns the effective agents policy.
func (b *Backend) agentsConfig() config.AgentsConfig {
	b.agentsMu.Lock()
	defer b.agentsMu.Unlock()
	return b.agents.WithDefaults()
}

// ListAgents implements capabilities.AgentSource. Native program status is
// authoritative; a block whose program reports no recognized status falls back
// to nexo's own process and screen observation. One row is reported per block,
// because the block is the pane nexo can focus.
func (b *Backend) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	if b == nil || b.Runner == nil {
		return nil, errors.New("rex agents requires a command runner")
	}
	resolved, err := b.server(ctx)
	if err != nil {
		return nil, err
	}
	cfg := b.agentsConfig()
	allow := agentdetect.AllowList(cfg.Processes)
	sessions, err := b.sessions(ctx)
	if err != nil {
		return nil, err
	}
	var present []string
	var native, fallback []agentSnapshot
	for _, session := range sessions {
		blocks, err := b.blocks(ctx, session.ID)
		if err != nil {
			return nil, err
		}
		for _, current := range blocks {
			if !terminalBlock(current) {
				continue
			}
			// Presence is recorded from the session listing itself, so a block
			// whose status or process is temporarily unreadable keeps its
			// durable state instead of looking like an exited agent.
			present = append(present, current.ID)
			snapshot, ok := b.observeAgent(ctx, session, current, cfg, allow)
			if !ok {
				continue
			}
			if snapshot.status == "" {
				fallback = append(fallback, snapshot)
			} else {
				native = append(native, snapshot)
			}
		}
	}
	states, err := b.recordAgentState(ctx, cfg, present, native, fallback)
	if err != nil {
		return nil, err
	}
	agents := make([]capabilities.Agent, 0, len(native)+len(fallback))
	for _, group := range [][]agentSnapshot{native, fallback} {
		for _, snapshot := range group {
			state := states[snapshot.block.ID]
			branch := ""
			if localBlock(snapshot.block, resolved) {
				branch = git.Branch(ctx, b.Runner, snapshot.path)
			}
			agents = append(agents, capabilities.Agent{
				PaneID:       snapshot.block.ID,
				Target:       snapshot.block.ID,
				ContainerID:  snapshot.session.ID,
				Container:    snapshot.session.Label,
				Window:       snapshot.block.Label,
				Harness:      snapshot.harness,
				State:        state.State,
				Seen:         state.Seen,
				StoppedSince: state.StoppedSince,
				Branch:       branch,
			})
		}
	}
	return agents, nil
}

// observeAgent reports the agent evidence for one terminal block. Native
// status wins; otherwise the block's processes and screen decide. A block whose
// status, process or screen cannot be read produces no observation, but its
// presence is already recorded, so its durable state survives.
func (b *Backend) observeAgent(ctx context.Context, session sessionInfo, current block, cfg config.AgentsConfig, allow map[string]struct{}) (agentSnapshot, bool) {
	records, err := b.programStatus(ctx, session.ID, current.ID)
	if err != nil {
		// A failed read is not a report that carries no status: the block may
		// still publish a native lifecycle, and the screen must not overwrite
		// a state nexo could not read.
		return agentSnapshot{}, false
	}
	live, _ := b.blockProcess(ctx, session.ID, current.ID)
	path := processPath(live)
	if status, harness, ok := selectStatusRecord(records, allow); ok {
		return agentSnapshot{session: session, block: current, harness: harness, status: status, path: path}, true
	}
	harness := processHarness(live, allow)
	if harness == "" {
		return agentSnapshot{}, false
	}
	screen, err := b.screen(ctx, session.ID, current.ID)
	if err != nil {
		return agentSnapshot{}, false
	}
	return agentSnapshot{
		session: session,
		block:   current,
		harness: harness,
		working: agentdetect.Working(harness, screen, cfg.WorkingPattern),
		path:    path,
	}, true
}

// recordAgentState turns the observed blocks into ledger state. Native and
// process/screen observations follow different transition policies, so each
// group is recorded with the complete present set: neither group prunes the
// other.
func (b *Backend) recordAgentState(ctx context.Context, cfg config.AgentsConfig, present []string, native, fallback []agentSnapshot) (map[string]agentstate.State, error) {
	ledger, err := b.agentLedger(ctx)
	if err != nil {
		return nil, err
	}
	states := make(map[string]agentstate.State, len(native)+len(fallback))
	if err := recordObservations(ledger, present, nativeObservations(native), agentstate.Policy{Native: true}, states); err != nil {
		return nil, err
	}
	if len(fallback) == 0 {
		return states, nil
	}
	policy := agentstate.Policy{IdleThreshold: time.Duration(cfg.IdleSeconds) * time.Second}
	if err := recordObservations(ledger, present, fallbackObservations(fallback), policy, states); err != nil {
		return nil, err
	}
	return states, nil
}

// recordObservations applies one policy to one group of observations and adds
// the resulting states to states, keyed by the identity they describe.
func recordObservations(ledger *agentstate.Ledger, present []string, observations []agentstate.Observation, policy agentstate.Policy, states map[string]agentstate.State) error {
	results, err := ledger.Observe(
		agentstate.Batch{Present: present, Observations: observations},
		policy,
		nil,
	)
	if err != nil {
		return err
	}
	for index, result := range results {
		states[observations[index].ID] = result.State
	}
	return nil
}

func nativeObservations(snapshots []agentSnapshot) []agentstate.Observation {
	observations := make([]agentstate.Observation, 0, len(snapshots))
	for _, snapshot := range snapshots {
		observations = append(observations, agentstate.Observation{
			ID:      snapshot.block.ID,
			Working: snapshot.status == "working",
			Status:  snapshot.status,
			// Rex's idle convention: an idle program waits at its prompt, so
			// nothing is waiting for the user. A focused block is only the
			// session's typing target, never proof that an app displays it.
			NativeSeen: snapshot.status == "idle",
		})
	}
	return observations
}

func fallbackObservations(snapshots []agentSnapshot) []agentstate.Observation {
	observations := make([]agentstate.Observation, 0, len(snapshots))
	for _, snapshot := range snapshots {
		observations = append(observations, agentstate.Observation{
			ID:      snapshot.block.ID,
			Working: snapshot.working,
		})
	}
	return observations
}

// ReadScreen reads the visible text of one terminal block. Rex returns the
// screen and scrollback as plain text, which the caller bounds for display.
func (b *Backend) ReadScreen(ctx context.Context, paneID string) (string, error) {
	if b == nil || b.Runner == nil {
		return "", errors.New("rex screen reader requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return "", errors.New("rex screen reader requires a pane id")
	}
	session, current, err := b.locateBlock(ctx, paneID)
	if err != nil {
		return "", err
	}
	return b.screen(ctx, session.ID, current.ID)
}

// FocusPane focuses one agent block: the block becomes its session's typing
// target, and the connected app is asked to show that session. The agent is
// marked seen only when an app really changed what the user sees.
func (b *Backend) FocusPane(ctx context.Context, paneID string) error {
	if b == nil || b.Runner == nil {
		return errors.New("rex pane focuser requires a command runner")
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return errors.New("rex pane focuser requires a pane id")
	}
	session, current, err := b.locateBlock(ctx, paneID)
	if err != nil {
		return err
	}
	_, err = b.api(ctx, "session.focus_block", blockFocusInput{SessionID: session.ID, BlockID: current.ID})
	if err != nil {
		return err
	}
	selected, err := b.selectSession(ctx, session.ID)
	if err != nil {
		return err
	}
	if !selected {
		// The approved no-app no-op: nothing moved the user's view, so the
		// agent has not been acknowledged.
		return nil
	}
	ledger, err := b.agentLedger(ctx)
	if err != nil {
		return err
	}
	return ledger.MarkSeen(current.ID)
}

// agentLedger scopes agent state to this Rex server run, the same scope
// container history uses: native block ids are only unique within one server.
func (b *Backend) agentLedger(ctx context.Context) (*agentstate.Ledger, error) {
	resolved, err := b.server(ctx)
	if err != nil {
		return nil, err
	}
	return agentstate.New(b.stateDir, "rex:"+resolved.namespace(), nil), nil
}

// locateBlock resolves a native block id to its owning session. Block ids are
// only unique within one server, so every match comes from the live listing.
func (b *Backend) locateBlock(ctx context.Context, blockID string) (sessionInfo, block, error) {
	blockID = strings.TrimSpace(blockID)
	if blockID == "" {
		return sessionInfo{}, block{}, errors.New("rex block id is empty")
	}
	sessions, err := b.sessions(ctx)
	if err != nil {
		return sessionInfo{}, block{}, err
	}
	for _, session := range sessions {
		blocks, err := b.blocks(ctx, session.ID)
		if err != nil {
			return sessionInfo{}, block{}, err
		}
		for _, current := range blocks {
			if current.ID == blockID {
				return session, current, nil
			}
		}
	}
	return sessionInfo{}, block{}, fmt.Errorf("rex block %q not found", blockID)
}

// selectStatusRecord reports the native state and configured harness of the
// record that names the block's agent. Status records are generic, so
// non-agent programs such as cargo or terraform are filtered out by nexo's
// configured harness policy.
func selectStatusRecord(records []statusRecord, allow map[string]struct{}) (string, string, bool) {
	best := -1
	var chosenStatus, chosenHarness string
	for _, record := range records {
		app := strings.TrimSpace(record.EffectiveApp)
		if app == "" {
			app = strings.TrimSpace(record.App)
		}
		harness, ok := recognisedApp(app, allow)
		if !ok {
			continue
		}
		status := normalizeStatus(record.State)
		rank := statusPriority[status]
		if rank <= best {
			continue
		}
		best, chosenStatus, chosenHarness = rank, status, harness
	}
	return chosenStatus, chosenHarness, best >= 0
}

// normalizeStatus returns one of the five native states. Rex documents an
// unknown state from a newer API as idle rather than a parse failure.
func normalizeStatus(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	if _, ok := statusPriority[normalized]; ok {
		return normalized
	}
	return "idle"
}

// recognisedApp resolves the harness a program-status app identity belongs to.
// The app name is the program's own identity, so a configured harness such as
// claude also covers the names its releases use, such as claude-code. The
// longest match wins so the result never depends on map order.
func recognisedApp(app string, allow map[string]struct{}) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(app))
	if name == "" {
		return "", false
	}
	harness := ""
	for candidate := range allow {
		if name != candidate && !strings.HasPrefix(name, candidate+"-") && !strings.HasPrefix(name, candidate+"_") {
			continue
		}
		if len(candidate) > len(harness) {
			harness = candidate
		}
	}
	return harness, harness != ""
}

// processChain lists the processes that can name a block's harness, the
// foreground program first.
func processChain(live processResult) []*processInfo {
	chain := make([]*processInfo, 0, 2+len(live.Ancestors))
	for _, process := range []*processInfo{live.Foreground, live.Child} {
		if process != nil {
			chain = append(chain, process)
		}
	}
	for index := range live.Ancestors {
		chain = append(chain, &live.Ancestors[index])
	}
	return chain
}

// processHarness returns the configured harness one of the block's processes
// runs. Process existence alone proves detection, not a lifecycle state.
func processHarness(live processResult, allow map[string]struct{}) string {
	for _, process := range processChain(live) {
		if harness := agentdetect.HarnessOf(process.Name, process.Script, allow); harness != "" {
			return harness
		}
	}
	return ""
}

// processPath is the directory the block is really working in, preferring the
// foreground program over the shell that started it.
func processPath(live processResult) string {
	for _, process := range processChain(live) {
		if path := strings.TrimSpace(process.CWD); path != "" {
			return path
		}
	}
	return ""
}

// blockFocusInput is the session.focus_block payload.
type blockFocusInput struct {
	SessionID string `json:"session_id"`
	BlockID   string `json:"block_id"`
}

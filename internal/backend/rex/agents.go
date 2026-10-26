package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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

// statusRecord is one native program-status report (OSC 7501). A block can hold
// several records: the root report plus one per child id.
type statusRecord struct {
	State        string `json:"state"`
	App          string `json:"app"`
	EffectiveApp string `json:"effective_app"`
}

type statusReport struct {
	Records []statusRecord `json:"records"`
}

type screenResult struct {
	Content string `json:"content"`
}

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
	status  string
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
	allow := harnessAllowList(cfg)
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
	states, err := b.recordAgentState(ctx, resolved, cfg, present, native, fallback)
	if err != nil {
		return nil, err
	}
	agents := make([]capabilities.Agent, 0, len(native)+len(fallback))
	for _, snapshot := range append(append([]agentSnapshot{}, native...), fallback...) {
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
	return agents, nil
}

// observeAgent reports the agent evidence for one terminal block.
func (b *Backend) observeAgent(ctx context.Context, session sessionInfo, current block, cfg config.AgentsConfig, allow map[string]struct{}) (agentSnapshot, bool) {
	records, err := b.programStatus(ctx, session.ID, current.ID)
	if err != nil {
		records = nil
	}
	live, _ := b.blockProcess(ctx, session.ID, current.ID)
	path := processPath(live)
	if record, harness, ok := selectStatusRecord(records, allow); ok {
		status, working := mapNativeStatus(record.State)
		return agentSnapshot{session: session, block: current, harness: harness, status: status, working: working, path: path}, true
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
func (b *Backend) recordAgentState(ctx context.Context, resolved identity, cfg config.AgentsConfig, present []string, native, fallback []agentSnapshot) (map[string]agentstate.State, error) {
	ledger := agentstate.New(b.stateDir, "rex:"+resolved.namespace(), nil)
	states := make(map[string]agentstate.State, len(native)+len(fallback))
	results, err := ledger.Observe(
		agentstate.Batch{Present: present, Observations: nativeObservations(native)},
		agentstate.Policy{Native: true},
		nil,
	)
	if err != nil {
		return nil, err
	}
	for index, result := range results {
		states[native[index].block.ID] = result.State
	}
	if len(fallback) == 0 {
		return states, nil
	}
	results, err = ledger.Observe(
		agentstate.Batch{Present: present, Observations: fallbackObservations(fallback)},
		agentstate.Policy{IdleThreshold: time.Duration(cfg.IdleSeconds) * time.Second},
		nil,
	)
	if err != nil {
		return nil, err
	}
	for index, result := range results {
		states[fallback[index].block.ID] = result.State
	}
	return states, nil
}

func nativeObservations(snapshots []agentSnapshot) []agentstate.Observation {
	observations := make([]agentstate.Observation, 0, len(snapshots))
	for _, snapshot := range snapshots {
		observations = append(observations, agentstate.Observation{
			ID:      snapshot.block.ID,
			Working: snapshot.working,
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
	resolved, err := b.server(ctx)
	if err != nil {
		return err
	}
	return agentstate.New(b.stateDir, "rex:"+resolved.namespace(), nil).MarkSeen(current.ID)
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

func (b *Backend) programStatus(ctx context.Context, sessionID, blockID string) ([]statusRecord, error) {
	output, err := b.blockMethod(ctx, sessionID, blockID, "program_status")
	if err != nil {
		return nil, err
	}
	var report statusReport
	if err := json.Unmarshal(output, &report); err != nil {
		return nil, fmt.Errorf("decode rex block %q program_status: %w", blockID, err)
	}
	return report.Records, nil
}

func (b *Backend) blockProcess(ctx context.Context, sessionID, blockID string) (processResult, error) {
	output, err := b.blockMethod(ctx, sessionID, blockID, "process")
	if err != nil {
		return processResult{}, err
	}
	var live processResult
	if err := json.Unmarshal(output, &live); err != nil {
		return processResult{}, fmt.Errorf("decode rex block %q process: %w", blockID, err)
	}
	return live, nil
}

func (b *Backend) screen(ctx context.Context, sessionID, blockID string) (string, error) {
	output, err := b.blockCall(ctx, sessionID, blockID, "format", map[string]any{"format": "text", "trim": true})
	if err != nil {
		return "", err
	}
	var decoded screenResult
	if err := json.Unmarshal(output, &decoded); err != nil {
		return "", fmt.Errorf("decode rex block %q format: %w", blockID, err)
	}
	return decoded.Content, nil
}

// selectStatusRecord picks the record that names the block's agent. Status
// records are generic, so non-agent programs such as cargo or terraform are
// filtered out by nexo's configured harness policy.
func selectStatusRecord(records []statusRecord, allow map[string]struct{}) (statusRecord, string, bool) {
	best := -1
	var chosen statusRecord
	chosenHarness := ""
	for _, record := range records {
		app := strings.TrimSpace(record.EffectiveApp)
		if app == "" {
			app = strings.TrimSpace(record.App)
		}
		harness, ok := recognisedApp(app, allow)
		if !ok {
			continue
		}
		rank := statusPriority[normalizeStatus(record.State)]
		if rank <= best {
			continue
		}
		best, chosen, chosenHarness = rank, record, harness
	}
	return chosen, chosenHarness, best >= 0
}

// mapNativeStatus maps a native state to the ledger's native status and to
// whether the agent is working. The specific state is preserved so that
// working, blocked and done transitions stay distinct.
func mapNativeStatus(state string) (string, bool) {
	normalized := normalizeStatus(state)
	return normalized, normalized == "working"
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

// harnessAllowList is nexo's configured harness policy as a lookup set.
func harnessAllowList(cfg config.AgentsConfig) map[string]struct{} {
	allow := make(map[string]struct{}, len(cfg.Processes))
	for _, process := range cfg.Processes {
		name := strings.ToLower(filepath.Base(strings.TrimSpace(process)))
		if name == "" || name == "." {
			continue
		}
		allow[name] = struct{}{}
	}
	return allow
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

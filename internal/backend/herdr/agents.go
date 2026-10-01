package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/agentstate"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/git"
	"github.com/vieitesss/nexo/internal/runner"
)

// Source reads Herdr's native agent awareness. It deliberately does not read
// pane output: Herdr owns agent detection, lifecycle state, and the idle/done
// seen semantics, so no long-lived event subscriber is needed here.
type Source struct {
	runner runner.CommandRunner
	now    func() time.Time

	mu       sync.Mutex
	stateDir string
	server   string
}

type herdrAgent struct {
	PaneID         string  `json:"pane_id"`
	WorkspaceID    string  `json:"workspace_id"`
	TabID          string  `json:"tab_id"`
	WorkspaceLabel string  `json:"workspace_label"`
	TabLabel       string  `json:"tab_label"`
	Agent          *string `json:"agent"`
	DisplayAgent   *string `json:"display_agent"`
	Status         string  `json:"agent_status"`
	Focused        bool    `json:"focused"`
	CWD            string  `json:"cwd"`
	ForegroundCWD  string  `json:"foreground_cwd"`
	StateChangeSeq uint64  `json:"state_change_seq"`
}

// NewAgentSource creates a Herdr AgentSource backed by the native agent API.
func NewAgentSource(r runner.CommandRunner) *Source {
	return &Source{
		runner: r,
		now:    time.Now,
	}
}

// SetClock replaces the clock used for stopped_since timestamps. It is useful
// for deterministic tests.
func (s *Source) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now == nil {
		s.now = time.Now
		return
	}
	s.now = now
}

// SetStateDir selects where this source stores per-server state and locks.
// It is intended for deterministic tests and does not alter the XDG default.
func (s *Source) SetStateDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateDir = dir
}

// ListAgents implements capabilities.AgentSource using Herdr's native state.
func (s *Source) ListAgents(ctx context.Context) ([]capabilities.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner == nil {
		return nil, errors.New("herdr agents requires a command runner")
	}
	output, err := s.runner.Run(ctx, "herdr", "agent", "list")
	if err != nil {
		return nil, fmt.Errorf("list herdr agents: %w", err)
	}
	native, err := decodeAgentList(output)
	if err != nil {
		return nil, err
	}
	workspaceLabels, tabLabels, err := s.locationLabels(ctx, native)
	if err != nil {
		return nil, err
	}
	if s.server == "" {
		s.server = herdrServerIdentity()
	}

	present := make([]string, 0, len(native))
	valid := make([]herdrAgent, 0, len(native))
	observations := make([]agentstate.Observation, 0, len(native))
	for _, current := range native {
		if current.PaneID != "" {
			present = append(present, current.PaneID)
		}
		state, ok := mapAgentStatus(current.Status)
		if !ok || current.PaneID == "" || agentName(current) == "" {
			// Herdr's unknown status means that this pane is not an agent for
			// nexo's purposes.
			continue
		}
		valid = append(valid, current)
		observations = append(observations, agentstate.Observation{
			ID:         current.PaneID,
			Working:    state == "working",
			Status:     current.Status,
			Sequence:   current.StateChangeSeq,
			NativeSeen: nativeSeen(current),
			Focused:    current.Focused,
		})
	}
	results, err := s.ledger().Observe(
		agentstate.Batch{Present: present, Observations: observations},
		agentstate.Policy{Native: true},
		nil,
	)
	if err != nil {
		return nil, err
	}
	agents := make([]capabilities.Agent, 0, len(results))
	for index, result := range results {
		current := valid[index]
		container := strings.TrimSpace(current.WorkspaceLabel)
		if container == "" {
			container = workspaceLabels[current.WorkspaceID]
		}
		window := strings.TrimSpace(current.TabLabel)
		if window == "" {
			window = tabLabels[current.TabID]
		}
		agents = append(agents, capabilities.Agent{
			PaneID:       current.PaneID,
			Target:       current.PaneID,
			ContainerID:  current.WorkspaceID,
			Container:    container,
			Window:       window,
			Harness:      agentName(current),
			State:        result.State.State,
			Seen:         result.State.Seen,
			StoppedSince: result.State.StoppedSince,
			Branch:       git.Branch(ctx, s.runner, herdrPath(current)),
		})
	}
	return agents, nil
}

func herdrPath(agent herdrAgent) string {
	path := strings.TrimSpace(agent.ForegroundCWD)
	if path == "" {
		path = strings.TrimSpace(agent.CWD)
	}
	return path
}

func nativeSeen(agent herdrAgent) bool {
	switch strings.ToLower(strings.TrimSpace(agent.Status)) {
	case "idle":
		return true
	case "blocked", "done":
		return false
	default:
		return false
	}
}

type herdrTab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

func (s *Source) locationLabels(ctx context.Context, agents []herdrAgent) (map[string]string, map[string]string, error) {
	workspaceLabels := make(map[string]string)
	tabLabels := make(map[string]string)
	needLookup := false
	for _, agent := range agents {
		if (strings.TrimSpace(agent.WorkspaceLabel) == "" && strings.TrimSpace(agent.WorkspaceID) != "") ||
			(strings.TrimSpace(agent.TabLabel) == "" && strings.TrimSpace(agent.TabID) != "") {
			needLookup = true
			break
		}
	}
	if !needLookup {
		return workspaceLabels, tabLabels, nil
	}
	workspaceOutput, err := s.runner.Run(ctx, "herdr", "workspace", "list")
	if err != nil {
		return nil, nil, fmt.Errorf("list herdr workspaces: %w", err)
	}
	workspaces, err := decodeWorkspaceList(workspaceOutput)
	if err != nil {
		return nil, nil, err
	}
	for _, workspace := range workspaces {
		workspaceLabels[workspace.ID] = workspace.Label
	}
	tabOutput, err := s.runner.Run(ctx, "herdr", "tab", "list")
	if err != nil {
		return nil, nil, fmt.Errorf("list herdr tabs: %w", err)
	}
	tabs, err := decodeTabList(tabOutput)
	if err != nil {
		return nil, nil, err
	}
	for _, tab := range tabs {
		tabLabels[tab.ID] = tab.Label
	}
	return workspaceLabels, tabLabels, nil
}

func decodeTabList(output []byte) ([]herdrTab, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode herdr tab list: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Type string     `json:"type"`
		Tabs []herdrTab `json:"tabs"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode herdr tab list: %w", err)
	}
	if result.Type != "" && result.Type != "tab_list" {
		return nil, fmt.Errorf("decode herdr tab list: unexpected result type %q", result.Type)
	}
	return result.Tabs, nil
}

func decodeAgentList(output []byte) ([]herdrAgent, error) {
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("decode herdr agent list: %w", err)
	}
	raw := output
	if len(envelope.Result) != 0 && string(envelope.Result) != "null" {
		raw = envelope.Result
	}
	var result struct {
		Type   string       `json:"type"`
		Agents []herdrAgent `json:"agents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode herdr agent list: %w", err)
	}
	if result.Type != "" && result.Type != "agent_list" {
		return nil, fmt.Errorf("decode herdr agent list: unexpected result type %q", result.Type)
	}
	return result.Agents, nil
}

func mapAgentStatus(status string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working":
		return "working", true
	case "idle", "blocked", "done":
		return "stopped", true
	default:
		return "", false
	}
}

func agentName(agent herdrAgent) string {
	if agent.Agent != nil && strings.TrimSpace(*agent.Agent) != "" {
		return strings.TrimSpace(*agent.Agent)
	}
	if agent.DisplayAgent != nil {
		return strings.TrimSpace(*agent.DisplayAgent)
	}
	return ""
}

func (s *Source) ledger() *agentstate.Ledger {
	return agentstate.New(s.stateDir, s.server, s.now)
}

func (s *Source) statePath(suffix string) string {
	return s.ledger().Path(suffix)
}

func herdrServerIdentity() string {
	if socket := strings.TrimSpace(os.Getenv("HERDR_SOCKET_PATH")); socket != "" {
		return socket
	}
	if configPath := strings.TrimSpace(os.Getenv("HERDR_CONFIG_PATH")); configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "herdr.sock")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return "default"
}

var _ capabilities.AgentSource = (*Source)(nil)

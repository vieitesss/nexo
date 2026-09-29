package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/runner"
)

// Source reads Herdr's native agent awareness. It deliberately does not read
// pane output: Herdr owns agent detection, lifecycle state, and the idle/done
// seen semantics, so no long-lived event subscriber is needed here.
type Source struct {
	runner runner.CommandRunner
	now    func() time.Time

	mu     sync.Mutex
	states map[string]agentState
}

type agentState struct {
	seen         bool
	stoppedSince *time.Time
}

type herdrAgent struct {
	PaneID       string  `json:"pane_id"`
	Agent        *string `json:"agent"`
	DisplayAgent *string `json:"display_agent"`
	Status       string  `json:"agent_status"`
	Focused      bool    `json:"focused"`
}

// NewAgentSource creates a Herdr AgentSource backed by the native agent API.
func NewAgentSource(r runner.CommandRunner) *Source {
	return &Source{
		runner: r,
		now:    time.Now,
		states: make(map[string]agentState),
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
	agents := make([]capabilities.Agent, 0, len(native))
	for _, current := range native {
		state, ok := mapAgentStatus(current.Status)
		if !ok || current.PaneID == "" || agentName(current) == "" {
			// Herdr's unknown status means that this pane is not an agent for
			// nexo's purposes.
			continue
		}
		tracked := s.states[current.PaneID]
		// Herdr uses idle/done to expose its per-client seen state: idle is
		// seen, while done is an unseen completion. A focused pane is always
		// seen, including a blocked pane.
		switch strings.ToLower(strings.TrimSpace(current.Status)) {
		case "idle":
			tracked.seen = true
		case "done":
			tracked.seen = false
		}
		if current.Focused {
			tracked.seen = true
		}
		if state == "working" {
			tracked.stoppedSince = nil
		} else if tracked.stoppedSince == nil {
			stoppedSince := s.now()
			tracked.stoppedSince = &stoppedSince
		}
		s.states[current.PaneID] = tracked
		agents = append(agents, capabilities.Agent{
			PaneID:       current.PaneID,
			Target:       current.PaneID,
			Harness:      agentName(current),
			State:        state,
			Seen:         tracked.seen,
			StoppedSince: tracked.stoppedSince,
		})
	}
	return agents, nil
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

var _ capabilities.AgentSource = (*Source)(nil)

package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

	mu       sync.Mutex
	stateDir string
	server   string
}

type agentState struct {
	Status         string     `json:"status"`
	StateChangeSeq uint64     `json:"state_change_seq"`
	Seen           bool       `json:"seen"`
	StoppedSince   *time.Time `json:"stopped_since,omitempty"`
}

type persistedStates struct {
	Agents map[string]agentState `json:"agents"`
}

type herdrAgent struct {
	PaneID         string  `json:"pane_id"`
	WorkspaceID    string  `json:"workspace_id"`
	TabID          string  `json:"tab_id"`
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
	if s.server == "" {
		s.server = herdrServerIdentity()
	}

	var agents []capabilities.Agent
	if err := s.updateStates(func(states map[string]agentState) error {
		agents = make([]capabilities.Agent, 0, len(native))
		for _, current := range native {
			state, ok := mapAgentStatus(current.Status)
			if !ok || current.PaneID == "" || agentName(current) == "" {
				// Herdr's unknown status means that this pane is not an agent for
				// nexo's purposes.
				continue
			}
			tracked := states[current.PaneID]
			statusChanged := tracked.Status != current.Status || tracked.StateChangeSeq != current.StateChangeSeq
			if tracked.Status == "" {
				statusChanged = true
			}
			if statusChanged {
				tracked.Status = current.Status
				tracked.StateChangeSeq = current.StateChangeSeq
				if state == "working" {
					tracked.StoppedSince = nil
					tracked.Seen = false
				} else {
					stoppedSince := s.now()
					tracked.StoppedSince = &stoppedSince
					tracked.Seen = nativeSeen(current)
				}
			} else if current.Focused {
				tracked.Seen = true
			}
			if state == "working" {
				tracked.StoppedSince = nil
			}
			if current.Focused {
				tracked.Seen = true
			}
			states[current.PaneID] = tracked
			branch := s.branch(ctx, current)
			agents = append(agents, capabilities.Agent{
				PaneID:       current.PaneID,
				Target:       current.PaneID,
				Harness:      agentName(current),
				State:        state,
				Seen:         tracked.Seen,
				StoppedSince: tracked.StoppedSince,
				Branch:       branch,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return agents, nil
}

func (s *Source) branch(ctx context.Context, agent herdrAgent) string {
	path := strings.TrimSpace(agent.ForegroundCWD)
	if path == "" {
		path = strings.TrimSpace(agent.CWD)
	}
	if path == "" {
		return ""
	}
	output, err := s.runner.Run(ctx, "git", "-C", path, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
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

func (s *Source) readStates() (map[string]agentState, error) {
	contents, err := os.ReadFile(s.statePath("json"))
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]agentState), nil
	}
	if err != nil {
		return nil, err
	}
	var saved persistedStates
	if err := json.Unmarshal(contents, &saved); err != nil {
		return nil, err
	}
	if saved.Agents == nil {
		saved.Agents = make(map[string]agentState)
	}
	return saved.Agents, nil
}

func (s *Source) updateStates(update func(map[string]agentState) error) error {
	stateDir := s.stateDir
	if stateDir == "" {
		stateDir = defaultStateDir()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create agent state directory: %w", err)
	}
	release, err := acquireFileLock(s.statePath("state.lock"))
	if err != nil {
		return fmt.Errorf("lock agent state: %w", err)
	}
	defer release()
	states, err := s.readStates()
	if err != nil {
		return fmt.Errorf("read agent state: %w", err)
	}
	if err := update(states); err != nil {
		return err
	}
	if err := writeStatesAtomic(s.statePath("json"), persistedStates{Agents: states}); err != nil {
		return fmt.Errorf("write agent state: %w", err)
	}
	return nil
}

func writeStatesAtomic(path string, states persistedStates) error {
	contents, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agents-state-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *Source) statePath(suffix string) string {
	dir := s.stateDir
	if dir == "" {
		dir = defaultStateDir()
	}
	return filepath.Join(dir, serverFileName(s.server)+"."+suffix)
}

func herdrServerIdentity() string {
	if socket := strings.TrimSpace(os.Getenv("HERDR_SOCKET_PATH")); socket != "" {
		return socket
	}
	return "default"
}

func defaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "nexo", "agents")
	}
	if dir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(dir, ".local", "state", "nexo", "agents")
	}
	return filepath.Join(os.TempDir(), "nexo", "agents")
}

func serverFileName(server string) string {
	if server == "" {
		server = "default"
	}
	digest := sha256.Sum256([]byte(server))
	return filepath.Base(server) + "-" + hex.EncodeToString(digest[:8])
}

func acquireFileLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create agent state directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open agent lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("flock agent lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

var _ capabilities.AgentSource = (*Source)(nil)

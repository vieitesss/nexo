// Package agentstate owns the durable agent-state ledger shared by backend
// observation adapters. Its interface is a normalized observation batch: the
// adapters retain native detection, while this module centralizes transition
// policy, disappearance cleanup, and locked atomic persistence.
package agentstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/statefile"
)

// State is the durable state associated with one agent identity. Status and
// Sequence retain native observations; LastIndicator is used by screen-based
// adapters that need an idle threshold.
type State struct {
	LastIndicator  time.Time  `json:"last_indicator,omitempty"`
	StoppedSince   *time.Time `json:"stopped_since,omitempty"`
	State          string     `json:"state,omitempty"`
	Seen           bool       `json:"seen"`
	Status         string     `json:"status,omitempty"`
	StateChangeSeq uint64     `json:"state_change_seq,omitempty"`
}

// Observation is one normalized backend observation. Native adapters set
// Status and Sequence and leave WorkingIndicator semantics to the native
// status mapping; screen adapters set Working and leave Status empty.
type Observation struct {
	ID         string
	Working    bool
	Status     string
	Sequence   uint64
	NativeSeen bool
	Focused    bool
}

// Batch contains all identities currently visible to an adapter and the
// observations that passed that adapter's native detection policy. Present
// includes identities that could not be fully observed, so their durable
// state is not removed during a transient observation failure.
type Batch struct {
	Present      []string
	Observations []Observation
}

// Policy selects the transition semantics for a batch.
type Policy struct {
	// Native uses the multiplexer-provided status and sequence. When false,
	// Working and IdleThreshold implement nexo's screen-observation policy.
	Native        bool
	IdleThreshold time.Duration
}

// Result pairs an observation with the state produced by the transition.
type Result struct {
	Observation Observation
	State       State
}

// Transition records a newly entered stopped state. It is supplied to the
// optional mutation callback while the ledger file is locked, allowing an
// adapter to retain backend-specific extensions atomically with the state.
type Transition struct {
	Observation Observation
	State       State
}

// Document is the persisted ledger document. Extra retains fields belonging
// to an adapter (for example, tmux's notification queue) without making those
// adapter concerns part of the ledger interface.
type Document struct {
	Agents map[string]State
	Extra  map[string]json.RawMessage
}

// Ledger is the state module's small interface to one backend identity.
type Ledger struct {
	stateDir string
	server   string
	now      func() time.Time
}

// New creates a ledger for one backend server. A nil clock uses time.Now.
func New(stateDir, server string, now func() time.Time) *Ledger {
	if now == nil {
		now = time.Now
	}
	return &Ledger{stateDir: stateDir, server: server, now: now}
}

// Path returns the path used by the ledger for a suffix. It is useful to
// adapters that need the watcher lock at the same seam.
func (l *Ledger) Path(suffix string) string {
	return statefile.Path(l.stateDir, l.server, suffix)
}

// Read loads the document without creating state-file artifacts.
func (l *Ledger) Read() (Document, error) {
	return l.read()
}

// ReadLocked loads the document while holding the per-server lock. Unlike
// Read, it creates the state directory because the lock itself is a durable
// coordination seam.
func (l *Ledger) ReadLocked() (Document, error) {
	if l.server == "" {
		return Document{}, errors.New("agent state has no server")
	}
	stateDir := l.stateDir
	if stateDir == "" {
		stateDir = statefile.DefaultDir()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return Document{}, fmt.Errorf("create agent state directory: %w", err)
	}
	release, err := statefile.Lock(l.Path("state.lock"), false)
	if err != nil {
		return Document{}, fmt.Errorf("lock agent state: %w", err)
	}
	defer release()
	document, err := l.read()
	if err != nil {
		return Document{}, fmt.Errorf("read agent state: %w", err)
	}
	return document, nil
}

// Update applies one mutation while holding the per-server lock and writes an
// atomic replacement. All persistent agent-state changes cross this seam.
func (l *Ledger) Update(update func(*Document) error) error {
	if l.server == "" {
		return errors.New("agent state has no server")
	}
	stateDir := l.stateDir
	if stateDir == "" {
		stateDir = statefile.DefaultDir()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create agent state directory: %w", err)
	}
	release, err := statefile.Lock(l.Path("state.lock"), false)
	if err != nil {
		return fmt.Errorf("lock agent state: %w", err)
	}
	defer release()
	document, err := l.read()
	if err != nil {
		return fmt.Errorf("read agent state: %w", err)
	}
	if err := update(&document); err != nil {
		return err
	}
	if document.Agents == nil {
		document.Agents = make(map[string]State)
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent state: %w", err)
	}
	if err := statefile.WriteAtomic(l.Path("json"), contents); err != nil {
		return fmt.Errorf("write agent state: %w", err)
	}
	return nil
}

// Observe applies the transition policy to one complete observation batch and
// returns the resulting states in observation order. The optional mutation is
// run under the same file lock after transitions, giving adapters a clean seam
// for backend-specific durable extensions.
func (l *Ledger) Observe(batch Batch, policy Policy, mutation func(*Document, []Transition) error) ([]Result, error) {
	var results []Result
	err := l.Update(func(document *Document) error {
		if document.Agents == nil {
			document.Agents = make(map[string]State)
		}
		present := make(map[string]struct{}, len(batch.Present))
		for _, id := range batch.Present {
			if id != "" {
				present[id] = struct{}{}
			}
		}
		for id := range document.Agents {
			if _, ok := present[id]; !ok {
				delete(document.Agents, id)
			}
		}

		now := l.now()
		results = make([]Result, 0, len(batch.Observations))
		transitions := make([]Transition, 0)
		for _, observation := range batch.Observations {
			if observation.ID == "" {
				continue
			}
			previous := document.Agents[observation.ID]
			current, enteredStopped := transition(previous, observation, policy, now)
			document.Agents[observation.ID] = current
			results = append(results, Result{Observation: observation, State: current})
			if enteredStopped {
				transitions = append(transitions, Transition{Observation: observation, State: current})
			}
		}
		if mutation != nil {
			if err := mutation(document, transitions); err != nil {
				return err
			}
		}
		return nil
	})
	return results, err
}

// MarkSeen marks a stopped identity seen. Unknown identities are a no-op and
// do not create a state directory, which is important for focus hooks.
func (l *Ledger) MarkSeen(id string) error {
	document, err := l.Read()
	if err != nil {
		return fmt.Errorf("read agent state: %w", err)
	}
	state, ok := document.Agents[id]
	if !ok || state.State != "stopped" {
		return nil
	}
	return l.Update(func(current *Document) error {
		state, ok := current.Agents[id]
		if !ok || state.State != "stopped" {
			return nil
		}
		state.Seen = true
		current.Agents[id] = state
		return nil
	})
}

func (l *Ledger) read() (Document, error) {
	contents, err := os.ReadFile(l.Path("json"))
	if errors.Is(err, os.ErrNotExist) {
		return Document{Agents: make(map[string]State), Extra: make(map[string]json.RawMessage)}, nil
	}
	if err != nil {
		return Document{}, err
	}
	var document Document
	if err := json.Unmarshal(contents, &document); err != nil {
		return Document{}, err
	}
	if document.Agents == nil {
		document.Agents = make(map[string]State)
	}
	if document.Extra == nil {
		document.Extra = make(map[string]json.RawMessage)
	}
	return document, nil
}

func transition(previous State, observation Observation, policy Policy, now time.Time) (State, bool) {
	if policy.Native {
		statusChanged := previous.Status != observation.Status || previous.StateChangeSeq != observation.Sequence
		if previous.Status == "" {
			statusChanged = true
		}
		enteredStopped := false
		if statusChanged {
			wasStopped := previous.Status != "" && !strings.EqualFold(strings.TrimSpace(previous.Status), "working")
			currentState := "stopped"
			if observation.Working {
				currentState = "working"
			} else if !wasStopped {
				enteredStopped = true
			}
			previous.Status = observation.Status
			previous.StateChangeSeq = observation.Sequence
			previous.State = currentState
			if observation.Working {
				previous.StoppedSince = nil
				previous.Seen = false
			} else {
				stoppedSince := now
				previous.StoppedSince = &stoppedSince
				previous.Seen = observation.NativeSeen
			}
		}
		if observation.Working {
			previous.State = "working"
			previous.StoppedSince = nil
		} else {
			previous.State = "stopped"
		}
		if observation.Focused {
			previous.Seen = true
		}
		return previous, enteredStopped
	}

	if previous.LastIndicator.IsZero() {
		previous.LastIndicator = now
		previous.State = "working"
		previous.Seen = false
	}
	if observation.Working {
		previous.LastIndicator = now
		previous.State = "working"
		previous.StoppedSince = nil
		previous.Seen = false
		return previous, false
	}
	if now.Sub(previous.LastIndicator) < policy.IdleThreshold {
		return previous, false
	}
	enteredStopped := previous.State != "stopped"
	previous.State = "stopped"
	if previous.StoppedSince == nil {
		stoppedAt := previous.LastIndicator.Add(policy.IdleThreshold)
		previous.StoppedSince = &stoppedAt
		previous.Seen = false
	}
	// An agent that stops in the pane the user is looking at has been seen;
	// no focus hook fires because focus never moved.
	if observation.Focused {
		previous.Seen = true
	}
	return previous, enteredStopped
}

func (d Document) MarshalJSON() ([]byte, error) {
	raw := make(map[string]json.RawMessage, len(d.Extra)+1)
	for key, value := range d.Extra {
		raw[key] = value
	}
	agents, err := json.Marshal(d.Agents)
	if err != nil {
		return nil, err
	}
	raw["agents"] = agents
	return json.Marshal(raw)
}

func (d *Document) UnmarshalJSON(contents []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(contents, &raw); err != nil {
		return err
	}
	agentsRaw, ok := raw["agents"]
	if ok && string(agentsRaw) != "null" {
		if err := json.Unmarshal(agentsRaw, &d.Agents); err != nil {
			return err
		}
	}
	delete(raw, "agents")
	d.Extra = raw
	if d.Agents == nil {
		d.Agents = make(map[string]State)
	}
	return nil
}

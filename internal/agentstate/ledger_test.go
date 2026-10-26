package agentstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestScreenObservationsPersistThresholdTransitions(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(stateDir, "tmux-test", func() time.Time { return now })
	policy := Policy{IdleThreshold: 3 * time.Second}

	results, err := ledger.Observe(Batch{
		Present:      []string{"%1"},
		Observations: []Observation{{ID: "%1", Working: true}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].State.State; got != "working" {
		t.Fatalf("first state = %q, want working", got)
	}

	now = now.Add(2 * time.Second)
	results, err = ledger.Observe(Batch{
		Present:      []string{"%1"},
		Observations: []Observation{{ID: "%1"}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].State.State; got != "working" {
		t.Fatalf("state before threshold = %q, want working", got)
	}

	now = now.Add(time.Second)
	results, err = ledger.Observe(Batch{
		Present:      []string{"%1"},
		Observations: []Observation{{ID: "%1"}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].State.State != "stopped" || results[0].State.StoppedSince == nil || !results[0].State.StoppedSince.Equal(now) {
		t.Fatalf("stopped result = %#v, want threshold timestamp %v", results[0].State, now)
	}

	now = now.Add(time.Hour)
	results, err = ledger.Observe(Batch{
		Present:      []string{"%1"},
		Observations: []Observation{{ID: "%1"}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].State.StoppedSince.Equal(now.Add(-time.Hour)) {
		t.Fatalf("stopped timestamp = %v, want durable timestamp", results[0].State.StoppedSince)
	}
}

func TestScreenObservationStoppingWhileFocusedIsSeen(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(t.TempDir(), "tmux-test", func() time.Time { return now })
	policy := Policy{IdleThreshold: 3 * time.Second}
	observe := func(observation Observation) State {
		t.Helper()
		results, err := ledger.Observe(Batch{Present: []string{observation.ID}, Observations: []Observation{observation}}, policy, nil)
		if err != nil {
			t.Fatal(err)
		}
		return results[0].State
	}

	observe(Observation{ID: "%1", Working: true, Focused: true})
	now = now.Add(3 * time.Second)
	if state := observe(Observation{ID: "%1", Focused: true}); state.State != "stopped" || !state.Seen {
		t.Fatalf("stopped while focused = %#v, want seen", state)
	}

	observe(Observation{ID: "%2", Working: true})
	now = now.Add(3 * time.Second)
	if state := observe(Observation{ID: "%2"}); state.State != "stopped" || state.Seen {
		t.Fatalf("stopped while unfocused = %#v, want unseen", state)
	}
	if state := observe(Observation{ID: "%2", Focused: true}); !state.Seen {
		t.Fatalf("stopped then focused = %#v, want seen", state)
	}
}

func TestNativeObservationsUseStatusSequenceAndFocus(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(stateDir, "herdr-test", func() time.Time { return now })
	policy := Policy{}

	results, err := ledger.Observe(Batch{
		Present: []string{"w:p1"},
		Observations: []Observation{{
			ID: "w:p1", Status: "idle", Sequence: 1, NativeSeen: true,
		}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].State.Seen || results[0].State.State != "stopped" {
		t.Fatalf("idle state = %#v, want seen stopped", results[0].State)
	}

	now = now.Add(time.Minute)
	results, err = ledger.Observe(Batch{
		Present: []string{"w:p1"},
		Observations: []Observation{{
			ID: "w:p1", Status: "blocked", Sequence: 2,
		}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].State.Seen || !results[0].State.StoppedSince.Equal(now) {
		t.Fatalf("blocked transition = %#v, want newly stopped unseen", results[0].State)
	}

	results, err = ledger.Observe(Batch{
		Present: []string{"w:p1"},
		Observations: []Observation{{
			ID: "w:p1", Status: "blocked", Sequence: 2, Focused: true,
		}},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].State.Seen {
		t.Fatalf("focused state = %#v, want seen", results[0].State)
	}
}

func TestObservePrunesDisappearedIdentitiesAndRunsMutationAtTheSeam(t *testing.T) {
	stateDir := t.TempDir()
	ledger := New(stateDir, "server", time.Now)
	_, err := ledger.Observe(Batch{
		Present:      []string{"one", "two"},
		Observations: []Observation{{ID: "one", Working: true}, {ID: "two", Working: true}},
	}, Policy{IdleThreshold: time.Second}, func(document *Document, _ []Transition) error {
		document.Extra["adapter_field"] = json.RawMessage(`{"kept":true}`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ledger.Observe(Batch{
		Present:      []string{"one"},
		Observations: []Observation{{ID: "one", Working: true}},
	}, Policy{IdleThreshold: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ledger.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := document.Agents["two"]; ok {
		t.Fatalf("agents = %#v, want disappeared identity removed", document.Agents)
	}
	var kept map[string]bool
	if err := json.Unmarshal(document.Extra["adapter_field"], &kept); err != nil || !kept["kept"] {
		t.Fatalf("extra fields = %#v, want adapter field preserved", document.Extra)
	}
}

func TestMarkSeenUnknownDoesNotCreateArtifacts(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	ledger := New(stateDir, "server", time.Now)
	if err := ledger.MarkSeen("unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state directory error = %v, want no artifacts", err)
	}
}

func TestMarkSeenMalformedStateReportsReadError(t *testing.T) {
	stateDir := t.TempDir()
	ledger := New(stateDir, "server", time.Now)
	if err := os.WriteFile(ledger.Path("json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ledger.MarkSeen("one")
	const want = "read agent state: unexpected end of JSON input"
	if err == nil || err.Error() != want {
		t.Fatalf("MarkSeen() error = %v, want %q", err, want)
	}
}

func TestMarkSeenUpdateFailureReportsLockError(t *testing.T) {
	stateDir := t.TempDir()
	ledger := New(stateDir, "server", time.Now)
	if err := ledger.Update(func(document *Document) error {
		document.Agents["one"] = State{State: "stopped"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lockPath := ledger.Path("state.lock")
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	err := ledger.MarkSeen("one")
	want := "lock agent state: open state lock: open " + lockPath + ": is a directory"
	if err == nil || err.Error() != want {
		t.Fatalf("MarkSeen() error = %v, want %q", err, want)
	}
}

func TestMalformedStateIsReportedThroughRead(t *testing.T) {
	stateDir := t.TempDir()
	ledger := New(stateDir, "server", time.Now)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger.Path("json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Read(); err == nil {
		t.Fatal("Read() succeeded for malformed state")
	}
}

func TestDocumentRoundTripKeepsAdapterFields(t *testing.T) {
	before := Document{
		Agents: map[string]State{"one": {State: "working", Seen: false}},
		Extra:  map[string]json.RawMessage{"pending": json.RawMessage(`[1,2]`)},
	}
	contents, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after Document
	if err := json.Unmarshal(contents, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip = %#v, want %#v", after, before)
	}
}

// observeOnce records one observation under a policy and returns its state.
func observeOnce(t *testing.T, ledger *Ledger, policy Policy, observation Observation) State {
	t.Helper()
	results, err := ledger.Observe(Batch{
		Present:      []string{observation.ID},
		Observations: []Observation{observation},
	}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	return results[0].State
}

func TestScreenObservationClearsNativeFields(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(t.TempDir(), "rex-test", func() time.Time { return now })
	if state := observeOnce(t, ledger, Policy{}, Observation{ID: "block", Status: "blocked"}); state.StoppedSince == nil {
		t.Fatalf("native state = %#v, want a stopped agent", state)
	}
	if err := ledger.MarkSeen("block"); err != nil {
		t.Fatal(err)
	}

	now = now.Add(10 * time.Second)
	state := observeOnce(t, ledger, Policy{IdleThreshold: 3 * time.Second}, Observation{ID: "block", Working: true})
	if state.State != "working" || state.Seen || state.StoppedSince != nil {
		t.Fatalf("screen state = %#v, want a working unseen agent", state)
	}
	if state.Status != "" || state.StateChangeSeq != 0 {
		t.Fatalf("screen state = %#v, want no native record left behind", state)
	}
}

func TestNativeObservationClearsScreenFields(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(t.TempDir(), "rex-test", func() time.Time { return now })
	observeOnce(t, ledger, Policy{IdleThreshold: 3 * time.Second}, Observation{ID: "block", Working: true})

	now = now.Add(10 * time.Second)
	state := observeOnce(t, ledger, Policy{}, Observation{ID: "block", Status: "idle", NativeSeen: true})
	if state.State != "stopped" || !state.Seen {
		t.Fatalf("native state = %#v, want the idle agent seen", state)
	}
	if !state.LastIndicator.IsZero() {
		t.Fatalf("native state = %#v, want no screen idle threshold left behind", state)
	}

	// A screen observation that follows starts its own idle window instead of
	// inheriting the native acknowledgement or the old threshold.
	now = now.Add(time.Second)
	state = observeOnce(t, ledger, Policy{IdleThreshold: 3 * time.Second}, Observation{ID: "block"})
	if state.State != "working" || state.Seen {
		t.Fatalf("screen state = %#v, want a fresh unseen observation", state)
	}
}

func TestOneBatchAppliesEachObservationsOwnMode(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(t.TempDir(), "rex-test", func() time.Time { return now })
	callbacks := 0

	results, err := ledger.Observe(Batch{
		Present: []string{"native-working", "native-stopped", "native-idle", "screen-working", "screen-idle"},
		Observations: []Observation{
			{ID: "native-working", Working: true, Status: "working", Sequence: 1},
			{ID: "native-stopped", Status: "blocked", Sequence: 2},
			{ID: "native-idle", Status: "idle", Sequence: 3, NativeSeen: true},
			{ID: "screen-working", Working: true},
			{ID: "screen-idle"},
		},
	}, Policy{IdleThreshold: 3 * time.Second}, func(*Document, []Transition) error {
		callbacks++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if callbacks != 1 {
		t.Fatalf("mutations = %d, want one transaction for the whole batch", callbacks)
	}

	want := []struct {
		id      string
		state   string
		seen    bool
		stopped bool
	}{
		{id: "native-working", state: "working"},
		{id: "native-stopped", state: "stopped", stopped: true},
		{id: "native-idle", state: "stopped", seen: true, stopped: true},
		{id: "screen-working", state: "working"},
		// A screen observation that shows an idle agent starts its own idle
		// window; only the threshold can stop it.
		{id: "screen-idle", state: "working"},
	}
	for index, test := range want {
		got := results[index].State
		if got.State != test.state || got.Seen != test.seen || (got.StoppedSince != nil) != test.stopped {
			t.Fatalf("%s state = %#v, want state %q seen %v stopped %v", test.id, got, test.state, test.seen, test.stopped)
		}
	}
	// One batch samples one clock instant: the native stop and the screen
	// window both date from it.
	if !results[1].State.StoppedSince.Equal(now) || !results[3].State.LastIndicator.Equal(now) {
		t.Fatalf("states = %#v, want every observation dated %v", results, now)
	}

	document, err := ledger.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Agents) != len(want) {
		t.Fatalf("agents = %#v, want every present identity in one document", document.Agents)
	}
	for _, test := range want {
		if _, ok := document.Agents[test.id]; !ok {
			t.Fatalf("agents = %#v, want %q durable in the same document", document.Agents, test.id)
		}
	}
}

func TestMixedBatchKeepsTheModeSwitchResets(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(t.TempDir(), "rex-test", func() time.Time { return now })
	policy := Policy{IdleThreshold: 3 * time.Second}
	observe := func(observations ...Observation) map[string]State {
		t.Helper()
		results, err := ledger.Observe(Batch{
			Present:      []string{"switch", "other"},
			Observations: observations,
		}, policy, nil)
		if err != nil {
			t.Fatal(err)
		}
		states := make(map[string]State, len(results))
		for index, result := range results {
			states[observations[index].ID] = result.State
		}
		return states
	}

	native := observe(
		Observation{ID: "switch", Status: "blocked", Sequence: 1},
		Observation{ID: "other", Working: true},
	)
	if native["switch"].State != "stopped" || native["switch"].StoppedSince == nil || !native["switch"].StoppedSince.Equal(now) {
		t.Fatalf("native state = %#v, want the native stop at %v", native["switch"], now)
	}
	if err := ledger.MarkSeen("switch"); err != nil {
		t.Fatal(err)
	}

	// Both identities switch mode in the same batch. The block that stops
	// publishing a native lifecycle loses its record, sequence, stopped age and
	// acknowledgement; the block that starts publishing one loses its screen
	// idle window.
	now = now.Add(10 * time.Second)
	switched := observe(
		Observation{ID: "switch", Working: true},
		Observation{ID: "other", Status: "idle", Sequence: 5, NativeSeen: true},
	)
	screen := switched["switch"]
	if screen.State != "working" || screen.Seen || screen.StoppedSince != nil {
		t.Fatalf("screen state = %#v, want a working unseen agent with no stopped age", screen)
	}
	if screen.Status != "" || screen.StateChangeSeq != 0 {
		t.Fatalf("screen state = %#v, want no native record left behind", screen)
	}
	if switched["other"].State != "stopped" || !switched["other"].Seen || switched["other"].StoppedSince == nil {
		t.Fatalf("native state = %#v, want the idle agent seen and stopped", switched["other"])
	}
	if !switched["other"].LastIndicator.IsZero() {
		t.Fatalf("native state = %#v, want no screen idle window left behind", switched["other"])
	}

	// Switching back is new work, not the report the user already
	// acknowledged, and the other block starts its own idle window.
	now = now.Add(10 * time.Second)
	resumed := observe(
		Observation{ID: "switch", Status: "blocked", Sequence: 9},
		Observation{ID: "other"},
	)
	if resumed["switch"].State != "stopped" || resumed["switch"].Seen {
		t.Fatalf("native state = %#v, want a fresh unseen stop", resumed["switch"])
	}
	if resumed["switch"].StoppedSince == nil || !resumed["switch"].StoppedSince.Equal(now) {
		t.Fatalf("stopped age = %v, want the fresh stop at %v", resumed["switch"].StoppedSince, now)
	}
	// The screen window starts again from this batch; only the threshold can
	// stop it, and the native acknowledgement is gone.
	if resumed["other"].State != "working" || resumed["other"].Status != "" || resumed["other"].StateChangeSeq != 0 {
		t.Fatalf("screen state = %#v, want a fresh screen window with no native record", resumed["other"])
	}
	if !resumed["other"].LastIndicator.Equal(now) {
		t.Fatalf("screen window = %v, want it restarted at %v", resumed["other"].LastIndicator, now)
	}
	if !resumed["switch"].LastIndicator.IsZero() {
		t.Fatalf("native state = %#v, want no screen idle window carried into the native record", resumed["switch"])
	}
}

func TestMixedBatchKeepsIdentitiesThatWereNotObserved(t *testing.T) {
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(t.TempDir(), "rex-test", func() time.Time { return now })
	policy := Policy{IdleThreshold: 3 * time.Second}
	present := []string{"native", "screen"}
	observe := func(batch Batch) Document {
		t.Helper()
		if _, err := ledger.Observe(batch, policy, nil); err != nil {
			t.Fatal(err)
		}
		document, err := ledger.Read()
		if err != nil {
			t.Fatal(err)
		}
		return document
	}

	first := observe(Batch{Present: present, Observations: []Observation{
		{ID: "native", Status: "blocked", Sequence: 1},
		{ID: "screen", Working: true},
	}})
	native := first.Agents["native"]
	if native.State != "stopped" || native.StoppedSince == nil {
		t.Fatalf("native state = %#v, want a stopped agent", native)
	}

	// The native observation failed for this poll, so its identity has no
	// observation but is still present: its durable state must survive.
	now = now.Add(10 * time.Second)
	failed := observe(Batch{Present: present, Observations: []Observation{
		{ID: "screen", Working: true},
	}})
	if !reflect.DeepEqual(failed.Agents["native"], native) {
		t.Fatalf("native state = %#v, want the unobserved identity preserved as %#v", failed.Agents["native"], native)
	}

	// No observation at all is not a disappearance either.
	now = now.Add(10 * time.Second)
	cold := observe(Batch{Present: present})
	if !reflect.DeepEqual(cold.Agents["native"], native) || cold.Agents["screen"].State != "working" {
		t.Fatalf("agents = %#v, want an empty observation batch to keep present identities", cold.Agents)
	}

	// A genuinely disappeared identity is pruned from the same document.
	now = now.Add(10 * time.Second)
	pruned := observe(Batch{Present: []string{"native"}})
	if _, ok := pruned.Agents["screen"]; ok {
		t.Fatalf("agents = %#v, want the disappeared identity pruned", pruned.Agents)
	}
	if !reflect.DeepEqual(pruned.Agents["native"], native) {
		t.Fatalf("native state = %#v, want the surviving identity preserved as %#v", pruned.Agents["native"], native)
	}
}

func TestMixedBatchMutationErrorWritesNothing(t *testing.T) {
	stateDir := t.TempDir()
	ledger := New(stateDir, "rex-test", time.Now)
	before, err := ledger.Observe(Batch{
		Present:      []string{"native", "screen"},
		Observations: []Observation{{ID: "native", Status: "working", Sequence: 1}, {ID: "screen", Working: true}},
	}, Policy{IdleThreshold: time.Second}, nil)
	if err != nil || len(before) != 2 {
		t.Fatalf("Observe() = %#v, %v; want the seeded batch", before, err)
	}
	seedPath := ledger.Path("json")
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}

	mutationErr := errors.New("adapter mutation failed")
	_, err = ledger.Observe(Batch{
		Present:      []string{"native", "screen"},
		Observations: []Observation{{ID: "native", Working: true}, {ID: "screen", Status: "blocked", Sequence: 2}},
	}, Policy{IdleThreshold: time.Second}, func(*Document, []Transition) error {
		return mutationErr
	})
	if !errors.Is(err, mutationErr) {
		t.Fatalf("Observe() error = %v, want the mutation error", err)
	}
	after, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(seed) {
		t.Fatalf("state after the failed mutation = %q, want the seed %q", after, seed)
	}
}

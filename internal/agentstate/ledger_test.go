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

func TestNativeObservationsUseStatusSequenceAndFocus(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	ledger := New(stateDir, "herdr-test", func() time.Time { return now })
	policy := Policy{Native: true}

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

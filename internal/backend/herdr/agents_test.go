package herdr

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

func TestAgentSourceMapsNativeStatuses(t *testing.T) {
	clock := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"id":"cli:agent:list","result":{"type":"agent_list","agents":[
			{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"working","focused":false,"revision":1},
			{"pane_id":"w:p2","workspace_id":"w","tab_id":"t","agent":"claude","agent_status":"idle","focused":false,"revision":2},
			{"pane_id":"w:p3","workspace_id":"w","tab_id":"t","agent":"codex","agent_status":"blocked","focused":false,"revision":3},
			{"pane_id":"w:p4","workspace_id":"w","tab_id":"t","agent":"aider","agent_status":"done","focused":false,"revision":4},
			{"pane_id":"w:p5","workspace_id":"w","tab_id":"t","agent":"unknown","agent_status":"unknown","focused":false,"revision":5}
		]}}`),
	}}
	source := NewAgentSource(runner)
	source.SetClock(func() time.Time { return clock })

	got, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ListAgents() returned %d agents, want 4: %#v", len(got), got)
	}
	want := []capabilities.Agent{
		{PaneID: "w:p1", Target: "w:p1", Harness: "pi", State: "working"},
		{PaneID: "w:p2", Target: "w:p2", Harness: "claude", State: "stopped", Seen: true, StoppedSince: timePtr(clock)},
		{PaneID: "w:p3", Target: "w:p3", Harness: "codex", State: "stopped", StoppedSince: timePtr(clock)},
		{PaneID: "w:p4", Target: "w:p4", Harness: "aider", State: "stopped", StoppedSince: timePtr(clock)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(runner.calls, []call{{name: "herdr", args: []string{"agent", "list"}}}) {
		t.Fatalf("calls = %#v, want only native agent list", runner.calls)
	}
}

func TestAgentSourceRetainsSeenAndStoppedSince(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"agent list": []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"idle","focused":true,"revision":1}]}}`),
	}}
	now := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	source := NewAgentSource(runner)
	source.SetClock(func() time.Time { return now })
	first, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	runner.outputs["agent list"] = []byte(`{"result":{"type":"agent_list","agents":[{"pane_id":"w:p1","workspace_id":"w","tab_id":"t","agent":"pi","agent_status":"blocked","focused":false,"revision":2}]}}`)
	second, err := source.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second[0].Seen || !second[0].StoppedSince.Equal(*first[0].StoppedSince) {
		t.Fatalf("second observation = %#v, want retained seen and stopped time", second[0])
	}
}

func timePtr(value time.Time) *time.Time { return &value }

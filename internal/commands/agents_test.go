package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/capabilities"
)

type fakeAgentSource struct {
	agents []capabilities.Agent
}

func (f fakeAgentSource) ListAgents(context.Context) ([]capabilities.Agent, error) {
	return f.agents, nil
}

func TestAgentsCommandJSONUsesAgentSchema(t *testing.T) {
	stopped := time.Date(2025, 1, 1, 12, 0, 3, 0, time.UTC)
	var output strings.Builder
	command, ok := NewRegistry().Lookup("agents")
	if !ok {
		t.Fatal("agents command is not registered")
	}
	err := command.Run(context.Background(), Invocation{
		Adapter: fakeAgentSource{agents: []capabilities.Agent{
			{PaneID: "%1", Target: "work:1.2", Harness: "pi", State: "stopped", StoppedSince: &stopped},
			{PaneID: "%2", Target: "work:1.3", Harness: "codex", State: "working"},
		}},
		Stdout: &output,
		JSON:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
		t.Fatalf("JSON output = %q: %v", output.String(), err)
	}
	if len(got) != 2 || got[0]["state"] != "stopped" || got[0]["seen"] != false {
		t.Fatalf("JSON output = %#v", got)
	}
	if _, ok := got[1]["stopped_since"]; ok {
		t.Fatalf("working agent has stopped_since: %#v", got[1])
	}
}

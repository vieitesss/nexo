package commands

import (
	"context"
	"encoding/json"
	"errors"
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

func TestAgentsWatchRejectsArguments(t *testing.T) {
	command, ok := NewRegistry().Lookup("agents")
	if !ok {
		t.Fatal("agents command is not registered")
	}
	err := command.Run(context.Background(), Invocation{Args: []string{"watch", "extra"}, Adapter: fakeAgentSource{}})
	var usage UsageError
	if !errors.As(err, &usage) || usage.Error() != "usage: nexo agents watch" {
		t.Fatalf("Run() error = %v, want usage error", err)
	}
}

func TestAgentsCommandJSONEmptyOutputIsArray(t *testing.T) {
	var output strings.Builder
	command, ok := NewRegistry().Lookup("agents")
	if !ok {
		t.Fatal("agents command is not registered")
	}
	if err := command.Run(context.Background(), Invocation{
		Adapter: fakeAgentSource{agents: []capabilities.Agent{}},
		Stdout:  &output,
		JSON:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "[]\n" {
		t.Fatalf("JSON output = %q, want %q", got, "[]\n")
	}
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
			{PaneID: "%1", Target: "work:1.2", ContainerID: "$1", Harness: "pi", State: "stopped", StoppedSince: &stopped},
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
	if len(got) != 2 || got[0]["state"] != "stopped" || got[0]["seen"] != false || got[0]["container_id"] != "$1" {
		t.Fatalf("JSON output = %#v", got)
	}
	if _, ok := got[1]["stopped_since"]; ok {
		t.Fatalf("working agent has stopped_since: %#v", got[1])
	}
}

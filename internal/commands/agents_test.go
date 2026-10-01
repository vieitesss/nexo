package commands

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/capabilities"
)

type fakeAgentSource struct {
	agents []capabilities.Agent
}

func (f fakeAgentSource) ListAgents(context.Context) ([]capabilities.Agent, error) {
	return f.agents, nil
}

func TestAgentsSeenUnknownPaneIsSilent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	adapter := tmuxbackend.New(funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			return []byte("/tmp/tmux-test\n"), nil
		}
		return nil, nil
	}), false)
	command, ok := NewRegistry().Lookup("agents")
	if !ok {
		t.Fatal("agents command is not registered")
	}
	var stdout, stderr strings.Builder
	err := command.Run(context.Background(), Invocation{
		Adapter: adapter,
		Args:    []string{"seen", "%999"},
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want silent success", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = %q / %q, want none", stdout.String(), stderr.String())
	}
}

func TestAgentsPickIsRemoved(t *testing.T) {
	command, ok := NewRegistry().Lookup("agents")
	if !ok {
		t.Fatal("agents command is not registered")
	}
	err := command.Run(context.Background(), Invocation{Args: []string{"pick"}, Adapter: fakeAgentSource{}, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), `unknown agents command "pick"`) {
		t.Fatalf("Run() error = %v, want removed entry point error", err)
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

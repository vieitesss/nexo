package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
)

func TestRemotePlaceCommandsListAgentsWithConfiguredBackend(t *testing.T) {
	var command string
	commands := remotePlaceCommands(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config: config.Config{Remote: config.RemoteConfig{
			Machines: []string{"dev"}, Backends: map[string]string{"dev": "tmux"},
		}},
		Runner: funcRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
			command = args[len(args)-1]
			return []byte("__NEXO_REMOTE_KIND__tmux\n$1\tapi\n__NEXO_AGENTS_START__\n[{\"container\":\"api\",\"state\":\"working\"}]\n__NEXO_AGENTS_END__\n"), nil
		}),
	})
	batch := commands[0]().(picker.RemoteBatch)
	if strings.Contains(command, "herdr' 'status") || !strings.Contains(command, "'nexo' '--backend' 'tmux' 'agents' '--json'") {
		t.Fatalf("remote command = %q, want configured tmux agent command without probe", command)
	}
	if len(batch.Agents) != 1 || batch.Agents[0].Container != "api" {
		t.Fatalf("agents = %#v, want remote tmux agent", batch.Agents)
	}
}

func TestRemotePlaceCommandsDeliverFastMachineWithoutWaitingForSlowMachine(t *testing.T) {
	slowRelease := make(chan struct{})
	commands := remotePlaceCommands(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"slow", "fast"}}},
		Runner: funcRunner(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
			machine := args[4]
			if machine == "slow" {
				select {
				case <-slowRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return []byte("$1\t" + machine + "\n"), nil
		}),
	})
	if len(commands) != 2 {
		t.Fatalf("commands = %d, want one per machine", len(commands))
	}
	fast := make(chan tea.Msg, 1)
	go func() { fast <- commands[1]() }()
	select {
	case message := <-fast:
		batch := message.(picker.RemoteBatch)
		if batch.Machine != "fast" || len(batch.Places) != 1 {
			t.Fatalf("batch = %#v, want fast result", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("fast machine waited for slow machine")
	}
	close(slowRelease)
}

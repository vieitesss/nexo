package commands

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/picker"
	"github.com/vieitesss/nexo/internal/runner"
)

func TestRemotePlaceCommandsDeliverFastMachineWithoutWaitingForSlowMachine(t *testing.T) {
	slowRelease := make(chan struct{})
	commands := remotePlaceCommands(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"slow", "fast"}}},
		Runner: runner.Func(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
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

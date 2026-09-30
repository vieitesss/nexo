package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/config"
)

type fakeTmuxInitializer struct{ configPath string }

func (f *fakeTmuxInitializer) Init(_ context.Context, input tmuxbackend.InitInput) error {
	f.configPath = input.ConfigPath
	return nil
}
func (f *fakeTmuxInitializer) ConfigureAgents(config.AgentsConfig) {}

func TestTmuxInitPassesConfigPathToAdapter(t *testing.T) {
	initializer := &fakeTmuxInitializer{}
	command, ok := NewRegistry().Lookup("tmux")
	if !ok {
		t.Fatal("tmux command is not registered")
	}
	if err := command.Run(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, ConfigPath: "/tmp/nexo.toml", Adapter: initializer, Args: []string{"init"}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if initializer.configPath != "/tmp/nexo.toml" {
		t.Fatalf("config path = %q", initializer.configPath)
	}
}

func TestTmuxInitRejectsOtherArguments(t *testing.T) {
	command, _ := NewRegistry().Lookup("tmux")
	if err := command.Run(context.Background(), Invocation{Selection: backend.Selection{Kind: backend.Tmux}, Adapter: &fakeTmuxInitializer{}, Args: []string{"watch"}}); err == nil {
		t.Fatal("Run() error = nil, want usage error")
	}
}

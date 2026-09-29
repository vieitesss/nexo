package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	tmuxbackend "github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/config"
)

type fakeTmuxInitializer struct {
	config     config.TmuxConfig
	configPath string
}

func (f *fakeTmuxInitializer) Init(_ context.Context, input tmuxbackend.InitInput) error {
	f.config = input.Config
	f.configPath = input.ConfigPath
	return nil
}

func (f *fakeTmuxInitializer) ConfigureAgents(config.AgentsConfig) {}

func TestTmuxInitPassesTmuxConfigToAdapter(t *testing.T) {
	initializer := &fakeTmuxInitializer{}
	command, ok := NewRegistry().Lookup("tmux")
	if !ok {
		t.Fatal("tmux command is not registered")
	}
	cfg := config.TmuxConfig{PlaceKey: "C-x"}
	if err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		Config:     config.Config{Tmux: cfg},
		ConfigPath: "/tmp/nexo.toml",
		Adapter:    initializer,
		Args:       []string{"init"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if initializer.config.PlaceKey != "C-x" || initializer.configPath != "/tmp/nexo.toml" {
		t.Fatalf("input = %#v, want tmux settings and config path", initializer)
	}
}

func TestTmuxInitRejectsOtherArguments(t *testing.T) {
	command, _ := NewRegistry().Lookup("tmux")
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   &fakeTmuxInitializer{},
		Args:      []string{"watch"},
	}); err == nil {
		t.Fatal("Run() error = nil, want usage error")
	}
}

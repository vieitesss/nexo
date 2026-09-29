package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
)

type fakeTmuxInitializer struct {
	config config.TmuxConfig
}

func (f *fakeTmuxInitializer) Init(_ context.Context, cfg config.TmuxConfig) error {
	f.config = cfg
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
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Tmux: cfg},
		Adapter:   initializer,
		Args:      []string{"init"},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if initializer.config.PlaceKey != "C-x" {
		t.Fatalf("config = %#v, want tmux settings", initializer.config)
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

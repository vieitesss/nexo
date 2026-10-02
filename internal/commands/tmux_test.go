package commands

import (
	"context"
	"errors"
	"strings"
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

type fakeGlanceController struct {
	action tmuxbackend.GlanceAction
	config config.GlanceConfig
}

func (f *fakeGlanceController) ConfigureGlance(cfg config.GlanceConfig) { f.config = cfg }

func (f *fakeGlanceController) Glance(_ context.Context, action tmuxbackend.GlanceAction) error {
	f.action = action
	return nil
}

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

func TestTmuxGlanceDispatchesActionAndConfig(t *testing.T) {
	controller := &fakeGlanceController{}
	command, _ := NewRegistry().Lookup("tmux")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   controller,
		Config:    config.Config{Glance: config.GlanceConfig{Tint: "red"}},
		Args:      []string{"glance", "toggle"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if controller.action != tmuxbackend.GlanceToggle {
		t.Fatalf("action = %q, want toggle", controller.action)
	}
	if controller.config.Tint != "red" {
		t.Fatalf("glance config = %#v, want the invocation config", controller.config)
	}
}

func TestTmuxGlanceRejectsUnknownAction(t *testing.T) {
	command, _ := NewRegistry().Lookup("tmux")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Adapter:   &fakeGlanceController{},
		Args:      []string{"glance", "maybe"},
	})
	var usageErr UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("Run() error = %v, want UsageError", err)
	}
}

func TestTmuxGlanceRequiresTmuxBackend(t *testing.T) {
	command, _ := NewRegistry().Lookup("tmux")
	err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Adapter:   &fakeGlanceController{},
		Args:      []string{"glance", "on"},
	})
	if err == nil || !strings.Contains(err.Error(), "tmux backend") {
		t.Fatalf("Run() error = %v, want tmux backend rejection", err)
	}
}

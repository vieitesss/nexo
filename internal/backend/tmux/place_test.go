package tmux

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

func TestPlaceProjectBuildsTmuxCommands(t *testing.T) {
	tests := []struct {
		behavior config.ProjectBehavior
		origin   string
		want     string
	}{
		{config.BehaviorSplitRight, "", "tmux -u split-window -h -c /src/api"},
		{config.BehaviorSplitDown, "%7", "tmux -u split-window -v -c /src/api -t %7"},
		{config.BehaviorTab, "", "tmux -u new-window -c /src/api"},
	}
	for _, test := range tests {
		t.Run(string(test.behavior), func(t *testing.T) {
			t.Setenv(capabilities.OriginPaneEnv, test.origin)
			var got string
			backend := &Backend{Runner: funcRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
				got = name + " " + strings.Join(args, " ")
				return nil, nil
			})}
			if err := backend.PlaceProject(context.Background(), "/src/api", test.behavior); err != nil {
				t.Fatalf("PlaceProject() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("command = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPlaceProjectRejectsFloat(t *testing.T) {
	backend := &Backend{Runner: funcRunner(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("float ran a tmux command")
		return nil, nil
	})}
	if err := backend.PlaceProject(context.Background(), "/src/api", config.BehaviorFloat); err == nil {
		t.Fatal("PlaceProject(float) error = nil, want unsupported")
	}
	for _, behavior := range backend.ProjectBehaviors() {
		if behavior == config.BehaviorFloat {
			t.Fatal("tmux advertises float")
		}
	}
}

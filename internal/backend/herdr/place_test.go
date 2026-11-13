package herdr

import (
	"context"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

func TestPlaceProjectBuildsHerdrCommands(t *testing.T) {
	tests := []struct {
		behavior config.ProjectBehavior
		origin   string
		want     string
	}{
		{config.BehaviorSplitRight, "", "pane split --direction right --cwd /src/api --focus"},
		{config.BehaviorSplitDown, "w1:p2", "pane split w1:p2 --direction down --cwd /src/api --focus"},
		{config.BehaviorTab, "w1:p2", "tab create --cwd /src/api --focus"},
	}
	for _, test := range tests {
		t.Run(string(test.behavior), func(t *testing.T) {
			t.Setenv(capabilities.OriginPaneEnv, test.origin)
			runner := &fakeRunner{}
			if err := New(runner).PlaceProject(context.Background(), "/src/api", test.behavior); err != nil {
				t.Fatalf("PlaceProject() error = %v", err)
			}
			if len(runner.calls) != 1 || runner.calls[0].name != "herdr" || strings.Join(runner.calls[0].args, " ") != test.want {
				t.Fatalf("calls = %#v, want herdr %s", runner.calls, test.want)
			}
		})
	}
}

func TestPlaceProjectRejectsFloat(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner)
	if err := backend.PlaceProject(context.Background(), "/src/api", config.BehaviorFloat); err == nil {
		t.Fatal("PlaceProject(float) error = nil, want unsupported")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("float ran %#v", runner.calls)
	}
	for _, behavior := range backend.ProjectBehaviors() {
		if behavior == config.BehaviorFloat {
			t.Fatal("herdr advertises float")
		}
	}
}

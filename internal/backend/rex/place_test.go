package rex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
)

func TestPlaceProjectBuildsRexCommands(t *testing.T) {
	tests := []struct {
		behavior config.ProjectBehavior
		origin   string
		want     string
	}{
		{config.BehaviorSplitRight, "", "--autostart=false split --split right -c /src/api"},
		{config.BehaviorSplitDown, "block:origin", "--autostart=false split --split below -c /src/api -b block:origin"},
		{config.BehaviorTab, "block:origin", "--autostart=false window new -c /src/api"},
	}
	for _, test := range tests {
		t.Run(string(test.behavior), func(t *testing.T) {
			t.Setenv(capabilities.OriginPaneEnv, test.origin)
			runner := &fakeRunner{outputs: map[string]string{test.want: ""}}
			if err := newTestBackend(t, runner, nil).PlaceProject(context.Background(), "/src/api", test.behavior); err != nil {
				t.Fatalf("PlaceProject() error = %v", err)
			}
			if keys := runner.keys(); len(keys) != 1 || keys[0] != test.want {
				t.Fatalf("commands = %#v, want %q", keys, test.want)
			}
		})
	}
}

func TestPlaceProjectFloatPassesPathAsJSON(t *testing.T) {
	t.Setenv("REX_SESSION", "session:1")
	path := `/src/odd "name"`
	var args map[string]string
	runner := &fakeRunner{handler: func(request call) ([]byte, bool) {
		// --autostart=false do -e SCRIPT --args JSON
		if len(request.args) != 6 || request.args[1] != "do" || request.args[2] != "-e" || request.args[4] != "--args" {
			return nil, false
		}
		if err := json.Unmarshal([]byte(request.args[5]), &args); err != nil {
			t.Fatalf("float args are not JSON: %v", err)
		}
		return nil, true
	}}
	if err := newTestBackend(t, runner, nil).PlaceProject(context.Background(), path, config.BehaviorFloat); err != nil {
		t.Fatalf("PlaceProject(float) error = %v", err)
	}
	if args["cwd"] != path || args["session_id"] != "session:1" {
		t.Fatalf("float args = %#v, want cwd %q in session:1", args, path)
	}
}

package backend

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/vieitesss/nexo/internal/backend/herdr"
	"github.com/vieitesss/nexo/internal/backend/rex"
	"github.com/vieitesss/nexo/internal/backend/tmux"
	"github.com/vieitesss/nexo/internal/config"
)

func TestResolveFlagOverridesConfigAndDetection(t *testing.T) {
	flag := "tmux"
	got, err := Resolve(context.Background(), ResolveInput{
		Flag:   &flag,
		Config: config.Config{Backend: "herdr"},
		Env:    mapEnvironment{"TMUX": "yes"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != Tmux || got.Source != "flag" {
		t.Fatalf("selection = %#v, want tmux from flag", got)
	}
}

func TestResolveConfigOverridesDetection(t *testing.T) {
	got, err := Resolve(context.Background(), ResolveInput{
		Config: config.Config{Backend: "herdr"},
		Env:    mapEnvironment{"TMUX": "yes"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != Herdr || got.Source != "config" {
		t.Fatalf("selection = %#v, want Herdr from config", got)
	}
}

func TestResolveAutoFlagSkipsConfig(t *testing.T) {
	flag := "auto"
	got, err := Resolve(context.Background(), ResolveInput{
		Flag:   &flag,
		Config: config.Config{Backend: "herdr"},
		Env:    mapEnvironment{"TMUX": "yes"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != Tmux || got.Source != "auto-detect" {
		t.Fatalf("selection = %#v, want tmux from auto-detect", got)
	}
}

func TestResolveRexKindFromFlagAndConfig(t *testing.T) {
	flag := "rex"
	got, err := Resolve(context.Background(), ResolveInput{Flag: &flag, Env: mapEnvironment{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != Rex || got.Source != "flag" {
		t.Fatalf("selection = %#v, want rex from flag", got)
	}
	if _, ok := got.Adapter.(*rex.Backend); !ok {
		t.Fatalf("adapter = %T, want a Rex backend", got.Adapter)
	}

	got, err = Resolve(context.Background(), ResolveInput{Config: config.Config{Backend: "rex"}, Env: mapEnvironment{"TMUX": "yes"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != Rex || got.Source != "config" {
		t.Fatalf("selection = %#v, want rex from config", got)
	}
}

func TestResolveInitializesAdapterAndCapabilities(t *testing.T) {
	flag := "tmux"
	got, err := Resolve(context.Background(), ResolveInput{
		Flag: &flag,
		Env:  mapEnvironment{},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Adapter == nil {
		t.Fatal("selection adapter = nil, want initialized adapter")
	}
	if len(got.Capabilities) == 0 {
		t.Fatal("selection capabilities are empty, want tmux capabilities")
	}
	if !sort.StringsAreSorted(got.Capabilities) {
		t.Fatalf("selection capabilities = %#v, want sorted names", got.Capabilities)
	}
}

func TestResolveReportsCompleteBackendCapabilityLists(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{name: "tmux", want: []string{
			"AgentSource", "ContainerHistory", "ContainerKiller", "ContainerLister", "ContainerOpener", "ContainerRenamer",
			"CurrentContainer", "CurrentProxyMachine", "FocusEventRecorder", "MessageDisplayer", "Notifier", "PaneFocuser",
			"ProjectOpener", "ProjectUseChecker", "RemotePaste", "RemoteProxy", "ScreenReader", "WorktreeCreator", "WorktreeExtractor",
		}},
		{name: "rex", want: []string{
			"AgentSource", "ContainerHistory", "ContainerKiller", "ContainerLister", "ContainerOpener", "ContainerRenamer",
			"CurrentContainer", "CurrentProxyMachine", "PaneFocuser", "ProjectOpener", "ProjectUseChecker", "RemoteProxy",
			"ScreenReader", "WorktreeCreator", "WorktreeExtractor",
		}},
		{name: "herdr", want: []string{
			"AgentSource", "ContainerHistory", "ContainerKiller", "ContainerLister", "ContainerOpener", "ContainerRenamer",
			"CurrentContainer", "CurrentProxyMachine", "FocusEventRecorder", "PaneFocuser", "ProjectOpener", "ProjectUseChecker",
			"RemotePaste", "RemoteProxy", "ScreenReader", "WorktreeCreator", "WorktreeExtractor",
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			flag := test.name
			got, err := Resolve(context.Background(), ResolveInput{Flag: &flag, Env: mapEnvironment{}})
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if !reflect.DeepEqual(got.Capabilities, test.want) {
				t.Fatalf("capabilities = %#v, want %#v", got.Capabilities, test.want)
			}
		})
	}
}

func TestResolveNoneHasNoAdapterOrCapabilities(t *testing.T) {
	got, err := Resolve(context.Background(), ResolveInput{Env: mapEnvironment{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != None {
		t.Fatalf("selection kind = %q, want none", got.Kind)
	}
	if got.Adapter != nil {
		t.Fatalf("selection adapter = %#v, want nil", got.Adapter)
	}
	if got.Capabilities == nil {
		t.Fatal("selection capabilities = nil, want empty list")
	}
}

func TestResolvePassesNameTemplate(t *testing.T) {
	template := "{org}/{repo}"
	for _, kind := range []string{"tmux", "herdr", "rex"} {
		for _, configured := range []*string{nil, &template} {
			t.Run(kind+fmt.Sprint(configured != nil), func(t *testing.T) {
				calls := 0
				r := fakeRunner(func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil })
				cfg := config.Config{Backend: kind, Session: config.SessionConfig{NameTemplate: configured}}
				selected, err := Resolve(context.Background(), ResolveInput{Config: cfg, Env: mapEnvironment{}, Runner: r})
				if err != nil {
					t.Fatal(err)
				}
				var got *string
				switch b := selected.Adapter.(type) {
				case *tmux.Backend:
					got = b.NameTemplate
				case *herdr.Backend:
					got = b.NameTemplate
				case *rex.Backend:
					got = b.NameTemplate
				default:
					t.Fatalf("adapter = %T", b)
				}
				if got != configured || calls != 0 {
					t.Fatalf("template = %v, want %v; selection calls = %d", got, configured, calls)
				}
			})
		}
	}
}

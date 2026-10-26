package backend

import (
	"context"
	"reflect"
	"sort"
	"testing"

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
			"ProjectOpener", "ProjectUseChecker", "RemoteKindProxy", "RemotePaste", "RemoteProxy", "ScreenReader", "WorktreeCreator", "WorktreeExtractor",
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

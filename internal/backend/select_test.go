package backend

import (
	"context"
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

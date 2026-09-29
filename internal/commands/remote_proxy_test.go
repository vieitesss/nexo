package commands

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/capabilities"
	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/runner"
)

type recordingRemoteProxy struct {
	opened [2]string
}

func (p *recordingRemoteProxy) Proxy(_ context.Context, machine, container string) error {
	p.opened = [2]string{machine, container}
	return nil
}

func TestRemoteOpenRoutesThroughConfiguredRemoteProxy(t *testing.T) {
	proxy := &recordingRemoteProxy{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   proxy,
		Args:      []string{"open", "devbox/$1"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if proxy.opened != [2]string{"devbox", "$1"} {
		t.Fatalf("proxy call = %#v, want devbox/$1", proxy.opened)
	}
}

type recordingKiller struct {
	closed string
}

func (k *recordingKiller) KillContainer(_ context.Context, id string) error {
	k.closed = id
	return nil
}

func (*recordingKiller) Proxy(context.Context, string, string) error { return nil }

func TestRemoteCloseOnlyKillsLocalProxy(t *testing.T) {
	killer := &recordingKiller{}
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   killer,
		Runner:    runner.Func(func(context.Context, string, ...string) ([]byte, error) { return nil, nil }),
		Args:      []string{"close", "devbox/$1"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if killer.closed != "devbox/$1" {
		t.Fatalf("closed target = %q, want local proxy name", killer.closed)
	}
}

func TestRemoteNewQuotesRemoteContainerName(t *testing.T) {
	proxy := &recordingRemoteProxy{}
	var got []string
	err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{Remote: config.RemoteConfig{Machines: []string{"devbox"}}},
		Adapter:   proxy,
		Runner: runner.Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "ssh" {
				t.Fatalf("command = %q, want ssh", name)
			}
			got = append([]string(nil), args...)
			return nil, nil
		}),
		Args: []string{"new", "devbox", "name with spaces"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(got[len(got)-1], "'name with spaces'") {
		t.Fatalf("remote command = %q, want quoted name", got[len(got)-1])
	}
	if !reflect.DeepEqual(proxy.opened, [2]string{"devbox", "name with spaces"}) {
		t.Fatalf("proxy call = %#v, want new target", proxy.opened)
	}
}

var _ capabilities.RemoteProxy = (*recordingRemoteProxy)(nil)
var _ capabilities.ContainerKiller = (*recordingKiller)(nil)

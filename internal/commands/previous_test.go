package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/containerstate"
)

type previousOpener struct{ id string }

func (o *previousOpener) OpenContainer(_ context.Context, id string) error {
	o.id = id
	return nil
}

type previousProxy struct{ machine, name string }

func (p *previousProxy) Proxy(_ context.Context, machine, name string) error {
	p.machine, p.name = machine, name
	return nil
}

func TestPreviousOpensLatestNonProxyContainer(t *testing.T) {
	dir := t.TempDir()
	store := containerstate.New(dir, "tmux", containerstate.LocalMachine())
	if err := store.Record(containerstate.Entry{ID: "$1", Name: "api", Machine: containerstate.LocalMachine()}); err != nil {
		t.Fatal(err)
	}
	opener := &previousOpener{}
	if err := (previousCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		StateDir:  dir,
		Adapter:   opener,
	}); err != nil {
		t.Fatal(err)
	}
	if opener.id != "$1" {
		t.Fatalf("opened id = %q, want $1", opener.id)
	}
}

func TestRemotePreviousReopensLatestProxyIdentity(t *testing.T) {
	dir := t.TempDir()
	store := containerstate.New(dir, "tmux", containerstate.LocalMachine())
	entry := containerstate.Entry{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	proxy := &previousProxy{}
	if err := (remoteCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		StateDir:  dir,
		Adapter:   proxy,
		Args:      []string{"previous"},
	}); err != nil {
		t.Fatal(err)
	}
	if proxy.machine != "devbox" || proxy.name != "api" {
		t.Fatalf("proxy target = %s/%s, want devbox/api", proxy.machine, proxy.name)
	}
}

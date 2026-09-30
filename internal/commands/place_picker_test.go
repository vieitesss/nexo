package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/picker"
)

type fakeContainerActionsAdapter struct {
	opened  string
	renamed [2]string
	killed  []string
}

func (f *fakeContainerActionsAdapter) OpenContainer(_ context.Context, id string) error {
	f.opened = id
	return nil
}

func (f *fakeContainerActionsAdapter) RenameContainer(_ context.Context, id, name string) error {
	f.renamed = [2]string{id, name}
	return nil
}

func (f *fakeContainerActionsAdapter) KillContainer(_ context.Context, id string) error {
	f.killed = append(f.killed, id)
	return nil
}

func TestLocalContainerResolverWrapsCurrentAdapter(t *testing.T) {
	adapter := &fakeContainerActionsAdapter{}
	actions, err := (localContainerResolver{adapter: adapter}).ResolveContainerActions(context.Background(), picker.LocalMachine())
	if err != nil {
		t.Fatalf("ResolveContainerActions() error = %v", err)
	}
	if err := actions.OpenContainer(context.Background(), "container-1"); err != nil {
		t.Fatalf("OpenContainer() error = %v", err)
	}
	if err := actions.RenameContainer(context.Background(), "container-1", "renamed"); err != nil {
		t.Fatalf("RenameContainer() error = %v", err)
	}
	if err := actions.KillContainer(context.Background(), "container-1"); err != nil {
		t.Fatalf("KillContainer() error = %v", err)
	}
	if adapter.opened != "container-1" || adapter.renamed != [2]string{"container-1", "renamed"} || len(adapter.killed) != 1 || adapter.killed[0] != "container-1" {
		t.Fatalf("adapter calls = %#v, want local wrapper delegation", adapter)
	}
}

func TestLocalContainerResolverRejectsRemoteMachine(t *testing.T) {
	_, err := (localContainerResolver{}).ResolveContainerActions(context.Background(), "remote")
	if err == nil {
		t.Fatal("ResolveContainerActions() accepted an unsupported remote machine")
	}
}

func TestRemotePickerOpensDisplayedContainerName(t *testing.T) {
	place := picker.Place{Machine: "devbox", ContainerID: "$1", ContainerName: "api"}
	if got, want := containerOpenID(place), "api"; got != want {
		t.Fatalf("container open target = %q, want displayed name %q", got, want)
	}
}

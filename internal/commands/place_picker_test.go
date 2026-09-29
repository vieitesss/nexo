package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
	"github.com/vieitesss/nexo/internal/config"
)

type fakePopup struct {
	command string
}

func (f *fakePopup) ShowPopup(_ context.Context, command string) error {
	f.command = command
	return nil
}

func TestPlacePickerUsesPopupCapabilityWithoutStartingAProgram(t *testing.T) {
	popup := &fakePopup{}
	command := placePickerCommand{}
	if err := command.Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Tmux},
		Config:    config.Config{},
		Adapter:   popup,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if popup.command == "" || popup.command == "nexo" {
		t.Fatalf("popup command = %q, want inline recursive command", popup.command)
	}
}

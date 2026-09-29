package commands

import (
	"context"
	"os"
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

func TestPlacePickerPassesBackendAndExplicitConfigToPopupChild(t *testing.T) {
	popup := &fakePopup{}
	command := placePickerCommand{}
	if err := command.Run(context.Background(), Invocation{
		Selection:  backend.Selection{Kind: backend.Tmux},
		Config:     config.Config{},
		ConfigPath: "/tmp/custom config.toml",
		Adapter:    popup,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "NEXO_PLACE_PICKER_INLINE=1 '" + os.Args[0] + "' --backend='tmux' --config-file='/tmp/custom config.toml'"
	if popup.command != want {
		t.Fatalf("popup command = %q, want exact argv %q", popup.command, want)
	}
}

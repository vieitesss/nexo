package commands

import (
	"context"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
)

type recordingFocus struct{ payload []byte }

func (f *recordingFocus) RecordFocusEvent(_ context.Context, payload []byte) error {
	f.payload = append([]byte(nil), payload...)
	return nil
}

func TestFocusCommandRecordsHerdrEventPayload(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", `{"type":"workspace_focused","workspace_id":"w1"}`)
	adapter := &recordingFocus{}
	if err := (focusCommand{}).Run(context.Background(), Invocation{
		Selection: backend.Selection{Kind: backend.Herdr},
		Adapter:   adapter,
	}); err != nil {
		t.Fatal(err)
	}
	if string(adapter.payload) != `{"type":"workspace_focused","workspace_id":"w1"}` {
		t.Fatalf("payload = %q, want the plugin event JSON", adapter.payload)
	}
}

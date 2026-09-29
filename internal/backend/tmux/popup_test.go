package tmux

import (
	"context"
	"reflect"
	"testing"
)

func TestShowPopupRunsDisplayPopupWithExitFlag(t *testing.T) {
	runner := &fakeRunner{}
	backend := New(runner, true)
	if err := backend.ShowPopup(context.Background(), "nexo --backend=tmux"); err != nil {
		t.Fatalf("ShowPopup() error = %v", err)
	}
	want := []call{{name: "tmux", args: []string{"display-popup", "-E", "nexo --backend=tmux"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

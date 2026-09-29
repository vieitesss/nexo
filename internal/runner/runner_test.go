package runner

import (
	"context"
	"errors"
	"testing"
)

func TestFuncRunnerRecordsExternalCall(t *testing.T) {
	var gotName string
	var gotArgs []string
	r := Func(func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("ok"), nil
	})

	out, err := r.Run(context.Background(), "tmux", "list-panes")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(out) != "ok" || gotName != "tmux" || len(gotArgs) != 1 || gotArgs[0] != "list-panes" {
		t.Fatalf("call = %q %q %v", out, gotName, gotArgs)
	}
}

func TestFuncRunnerPreservesFailure(t *testing.T) {
	want := errors.New("failed")
	r := Func(func(context.Context, string, ...string) ([]byte, error) { return nil, want })
	if _, err := r.Run(context.Background(), "tmux"); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
}

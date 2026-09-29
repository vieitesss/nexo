package remote

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vieitesss/nexo/internal/backend"
)

func TestListMachineUsesSSHBatchModeAndOptionalConfig(t *testing.T) {
	var gotName string
	var gotArgs []string
	lister := NewLister(fakeRunner(func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, append([]string(nil), args...)
		return []byte("$1\tdev\n"), nil
	}), backend.Tmux, "/tmp/ssh config")

	result := lister.ListMachine(context.Background(), "devbox")
	if result.Error != "" || len(result.Containers) != 1 || result.Containers[0].Machine != "devbox" {
		t.Fatalf("result = %#v, want one remote container", result)
	}
	if gotName != "ssh" {
		t.Fatalf("runner name = %q, want ssh", gotName)
	}
	want := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-F", "/tmp/ssh config", "devbox", "tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}"}
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ssh args = %#v, want %#v", gotArgs, want)
	}
}

func TestListMachineDecodesHerdrWorkspaces(t *testing.T) {
	lister := NewLister(fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"}]}}`), nil
	}), backend.Herdr, "")
	result := lister.ListMachine(context.Background(), "gpu1")
	if result.Error != "" || len(result.Containers) != 1 || result.Containers[0].ID != "w1" || result.Containers[0].Machine != "gpu1" {
		t.Fatalf("result = %#v, want one Herdr container", result)
	}
}

func TestListRunsMachinesInParallelAndKeepsFastResultIndependent(t *testing.T) {
	started := make(chan string, 2)
	slowRelease := make(chan struct{})
	lister := NewLister(fakeRunner(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		machine := args[4]
		started <- machine
		if machine == "slow" {
			select {
			case <-slowRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []byte("$1\t" + machine + "\n"), nil
	}), backend.Tmux, "")
	lister.Timeout = 50 * time.Millisecond

	done := make(chan []MachineResult, 1)
	go func() { done <- lister.List(context.Background(), []string{"slow", "fast"}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("machines did not start concurrently")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("machines did not start concurrently")
	}
	close(slowRelease)
	select {
	case results := <-done:
		if results[1].Machine != "fast" || len(results[1].Containers) != 1 {
			t.Fatalf("results = %#v, want fast machine result", results)
		}
	case <-time.After(time.Second):
		t.Fatal("listing waited unexpectedly long")
	}
}

func TestListMachineReportsTimeoutAsMachineError(t *testing.T) {
	lister := NewLister(fakeRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), backend.Tmux, "")
	lister.Timeout = 5 * time.Millisecond
	result := lister.ListMachine(context.Background(), "offline")
	if !strings.Contains(result.Error, "timed out") {
		t.Fatalf("error = %q, want timeout reason", result.Error)
	}
}

type fakeRunner func(context.Context, string, ...string) ([]byte, error)

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

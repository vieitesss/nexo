package remote

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/backend"
)

func TestListMachineDetectsHerdrKindAndDecodesOneRoundTrip(t *testing.T) {
	calls := 0
	orchestrator := newTestOrchestrator(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		command := args[len(args)-1]
		if !strings.Contains(command, "'herdr' 'status' 'server' '--json'") || !strings.Contains(command, "'herdr' 'workspace' 'list'") {
			t.Fatalf("remote command = %q, want Herdr probe and listing", command)
		}
		return []byte(remoteKindFrameStart + string(KindHerdr) + "\n" + `{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"}]}}` + "\n"), nil
	}), backend.Tmux, "")
	result := listMachine(orchestrator, "gpu1")
	if calls != 1 || result.Error != "" || result.Kind != KindHerdr || len(result.Containers) != 1 || result.Containers[0].ID != "w1" {
		t.Fatalf("result = %#v, calls = %d, want one Herdr listing", result, calls)
	}
}

func TestSplitRemoteKindIgnoresMarkersAfterProbeFrame(t *testing.T) {
	output := []byte(remoteKindFrameStart + string(KindTmux) + "\n$1\\tapi\n" + agentFrameStart + "\n" + remoteKindFrameStart + string(KindHerdr) + "\n" + agentFrameEnd + "\n")
	kind, listing := splitRemoteKind(output, backend.Herdr)
	if kind != KindTmux {
		t.Fatalf("kind = %q, want tmux from the first probe frame", kind)
	}
	if !strings.Contains(string(listing), remoteKindFrameStart+string(KindHerdr)) {
		t.Fatal("later marker was removed from listing instead of remaining payload")
	}
}

func TestDetectedTmuxNoServerRetainsKindAndEmptyListing(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(remoteKindFrameStart + string(KindTmux) + "\n"), &exec.ExitError{Stderr: []byte("no server running")}
	}), backend.Herdr, "")
	result := listMachine(orchestrator, "offline")
	if result.Error != "" || result.Kind != KindTmux || len(result.Containers) != 0 {
		t.Fatalf("result = %#v, want successful empty tmux listing", result)
	}
}

func TestListCachesDetectedKindPerMachineResult(t *testing.T) {
	orchestrator := newTestOrchestrator(fakeRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[4] == "herdr-host" {
			return []byte(remoteKindFrameStart + string(KindHerdr) + "\n{" + `"result":{"type":"workspace_list","workspaces":[]}` + "}\n"), nil
		}
		return []byte(remoteKindFrameStart + string(KindTmux) + "\n$1\tapi\n"), nil
	}), backend.Herdr, "")
	results := orchestrator.List(context.Background(), []string{"herdr-host", "tmux-host"})
	if len(results) != 2 || results[0].Kind != KindHerdr || results[1].Kind != KindTmux {
		t.Fatalf("results = %#v, want one cached kind per machine", results)
	}
}

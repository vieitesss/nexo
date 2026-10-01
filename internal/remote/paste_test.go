package remote

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type pasteCall struct {
	name string
	args []string
}

type pasteRunner struct {
	calls  []pasteCall
	output []byte
	err    error
	input  []byte
}

func (r *pasteRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, pasteCall{name: name, args: append([]string(nil), args...)})
	return r.output, r.err
}

func (r *pasteRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, pasteCall{name: name, args: append([]string(nil), args...)})
	r.input = append([]byte(nil), input...)
	return r.output, r.err
}

func TestClipboardReaderUsesWaylandPNGReader(t *testing.T) {
	runner := &pasteRunner{output: []byte("png")}
	reader := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(key string) string {
		if key == "WAYLAND_DISPLAY" {
			return "wayland-1"
		}
		return ""
	}}
	got, err := reader.Read(context.Background())
	if err != nil || string(got) != "png" {
		t.Fatalf("Read() = %q, %v; want png", got, err)
	}
	if !reflect.DeepEqual(runner.calls[0], pasteCall{name: "wl-paste", args: []string{"--type", "image/png", "--no-newline"}}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestClipboardReaderTreatsClipboardAbsenceAsSilent(t *testing.T) {
	runner := &pasteRunner{err: errors.New("clipboard does not contain image/png")}
	reader := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "" }}
	got, err := reader.Read(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Read() = %q, %v; want silent absence", got, err)
	}
}

func TestClipboardReaderReportsMissingTool(t *testing.T) {
	runner := &pasteRunner{err: errors.New("exec: xclip: executable file not found in $PATH")}
	reader := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "" }}
	_, err := reader.Read(context.Background())
	var missing *MissingToolError
	if !errors.As(err, &missing) || missing.Tool != "xclip" {
		t.Fatalf("Read() error = %v, want missing xclip", err)
	}
}

func TestUploadPNGUsesConfiguredSSHAndSecureRemoteName(t *testing.T) {
	runner := &pasteRunner{output: []byte("/tmp/nexo-paste-Ab12.png\n")}
	path, err := UploadPNG(context.Background(), runner, "/tmp/ssh config", "devbox", []byte("png"))
	if err != nil || path != "/tmp/nexo-paste-Ab12.png" {
		t.Fatalf("UploadPNG() = %q, %v", path, err)
	}
	if !reflect.DeepEqual(runner.input, []byte("png")) {
		t.Fatalf("input = %q, want png", runner.input)
	}
	if runner.calls[0].name != "ssh" || !containsArgs(runner.calls[0].args, "-F", "/tmp/ssh config") || !strings.Contains(runner.calls[0].args[len(runner.calls[0].args)-1], "nexo-paste-") {
		t.Fatalf("ssh call = %#v", runner.calls[0])
	}
}

func TestUploadPNGRejectsUnsafeRemoteName(t *testing.T) {
	runner := &pasteRunner{output: []byte("/tmp/not-safe\n")}
	if _, err := UploadPNG(context.Background(), runner, "", "devbox", []byte("png")); err == nil {
		t.Fatal("UploadPNG() error = nil, want unsafe path error")
	}
}

func containsArgs(args []string, values ...string) bool {
	for i := 0; i+len(values) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(values)], values) {
			return true
		}
	}
	return false
}

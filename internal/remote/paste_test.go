package remote

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type pasteCall struct {
	name string
	args []string
}

type pasteRunner struct {
	calls   []pasteCall
	outputs map[string][]byte
	fail    map[string]error
	output  []byte
	input   []byte
}

func (r *pasteRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return r.record(name, args)
}

func (r *pasteRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	r.input = append([]byte(nil), input...)
	return r.record(name, args)
}

func (r *pasteRunner) record(name string, args []string) ([]byte, error) {
	r.calls = append(r.calls, pasteCall{name: name, args: append([]string(nil), args...)})
	key := name + " " + strings.Join(args, " ")
	if err, ok := r.fail[key]; ok {
		return nil, err
	}
	if output, ok := r.outputs[key]; ok {
		return output, nil
	}
	return r.output, nil
}

func missingTool(name string) error {
	return errors.New("exec: " + name + ": executable file not found in $PATH")
}

var testPNG = []byte("\x89PNG\r\n\x1a\nfake png payload")

const (
	waylandList  = "wl-paste --list-types"
	waylandRead  = "wl-paste --type image/png --no-newline"
	xclipList    = "xclip -selection clipboard -target TARGETS -out"
	xclipRead    = "xclip -selection clipboard -target image/png -out"
	darwinPaste  = "pngpaste -"
	darwinScript = "osascript -e set pngData to the clipboard as «class PNGf»\nreturn pngData"
)

func TestClipboardReaderWaylandReadsPNGOnlyWhenOffered(t *testing.T) {
	runner := &pasteRunner{outputs: map[string][]byte{
		waylandList: []byte("text/plain\nimage/png\n"),
		waylandRead: testPNG,
	}}
	reader := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(key string) string {
		if key == "WAYLAND_DISPLAY" {
			return "wayland-1"
		}
		return ""
	}}
	got, err := reader.Read(context.Background())
	if err != nil || !bytes.Equal(got, testPNG) {
		t.Fatalf("Read() = %q, %v; want PNG", got, err)
	}
	want := []pasteCall{
		{name: "wl-paste", args: []string{"--list-types"}},
		{name: "wl-paste", args: []string{"--type", "image/png", "--no-newline"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClipboardReaderWaylandSkipsNonImageOffer(t *testing.T) {
	runner := &pasteRunner{outputs: map[string][]byte{waylandList: []byte("text/plain\ntext/html\n")}}
	reader := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "wayland-1" }}
	got, err := reader.Read(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Read() = %q, %v; want silent absence", got, err)
	}
	want := []pasteCall{{name: "wl-paste", args: []string{"--list-types"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want only format discovery", runner.calls)
	}
}

func TestClipboardReaderX11UsesTargetsAndPNGTarget(t *testing.T) {
	runner := &pasteRunner{outputs: map[string][]byte{
		xclipList: []byte("TARGETS\nimage/png\n"),
		xclipRead: testPNG,
	}}
	got, err := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "" }}.Read(context.Background())
	if err != nil || !bytes.Equal(got, testPNG) {
		t.Fatalf("Read() = %q, %v; want PNG", got, err)
	}
	want := []pasteCall{
		{name: "xclip", args: []string{"-selection", "clipboard", "-target", "TARGETS", "-out"}},
		{name: "xclip", args: []string{"-selection", "clipboard", "-target", "image/png", "-out"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestClipboardReaderTreatsClipboardAbsenceAsSilent(t *testing.T) {
	runner := &pasteRunner{fail: map[string]error{xclipList: errors.New("clipboard does not contain image/png")}}
	got, err := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "" }}.Read(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Read() = %q, %v; want silent absence", got, err)
	}
}

func TestClipboardReaderRejectsNonPNGPayload(t *testing.T) {
	runner := &pasteRunner{outputs: map[string][]byte{
		xclipList: []byte("image/png"),
		xclipRead: []byte("this is not a png"),
	}}
	got, err := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "" }}.Read(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Read() = %q, %v; want invalid data treated as absence", got, err)
	}
}

func TestClipboardReaderReportsMissingTool(t *testing.T) {
	runner := &pasteRunner{fail: map[string]error{xclipList: missingTool("xclip")}}
	_, err := ClipboardReader{Runner: runner, GOOS: "linux", Env: func(string) string { return "" }}.Read(context.Background())
	var missing *MissingToolError
	if !errors.As(err, &missing) || missing.Tool != "xclip" {
		t.Fatalf("Read() error = %v, want missing xclip", err)
	}
}

func TestClipboardReaderDarwinUsesPngpaste(t *testing.T) {
	runner := &pasteRunner{outputs: map[string][]byte{darwinPaste: testPNG}}
	got, err := ClipboardReader{Runner: runner, GOOS: "darwin"}.Read(context.Background())
	if err != nil || !bytes.Equal(got, testPNG) {
		t.Fatalf("Read() = %q, %v; want pngpaste PNG", got, err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "pngpaste" {
		t.Fatalf("calls = %#v, want only pngpaste", runner.calls)
	}
}

func TestClipboardReaderDarwinDecodesAppleScriptData(t *testing.T) {
	appleScriptOutput := []byte("«data PNGf" + strings.ToUpper(hex.EncodeToString(testPNG)) + "»\n")
	runner := &pasteRunner{
		outputs: map[string][]byte{darwinScript: appleScriptOutput},
		fail:    map[string]error{darwinPaste: missingTool("pngpaste")},
	}
	got, err := ClipboardReader{Runner: runner, GOOS: "darwin"}.Read(context.Background())
	if err != nil || !bytes.Equal(got, testPNG) {
		t.Fatalf("Read() = %q, %v; want decoded AppleScript PNG", got, err)
	}
}

func TestClipboardReaderDarwinRejectsAppleScriptText(t *testing.T) {
	runner := &pasteRunner{
		outputs: map[string][]byte{darwinScript: []byte("«data PNGf47494638»\n")},
		fail:    map[string]error{darwinPaste: missingTool("pngpaste")},
	}
	got, err := ClipboardReader{Runner: runner, GOOS: "darwin"}.Read(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Read() = %q, %v; want non-PNG data treated as absence", got, err)
	}
}

func TestClipboardReaderDarwinReportsBothToolsMissing(t *testing.T) {
	runner := &pasteRunner{fail: map[string]error{
		darwinPaste:  missingTool("pngpaste"),
		darwinScript: missingTool("osascript"),
	}}
	_, err := ClipboardReader{Runner: runner, GOOS: "darwin"}.Read(context.Background())
	var missing *MissingToolError
	if !errors.As(err, &missing) || missing.Tool != "pngpaste or osascript" {
		t.Fatalf("Read() error = %v, want both tools missing", err)
	}
}

func TestUploadPNGUsesConfiguredSSHAndSecureRemoteName(t *testing.T) {
	runner := &pasteRunner{output: []byte("/tmp/nexo-paste-Ab12/nexo-paste-image.png\n")}
	path, err := UploadPNG(context.Background(), runner, "/tmp/ssh config", "devbox", []byte("png"))
	if err != nil || path != "/tmp/nexo-paste-Ab12/nexo-paste-image.png" {
		t.Fatalf("UploadPNG() = %q, %v", path, err)
	}
	if !reflect.DeepEqual(runner.input, []byte("png")) {
		t.Fatalf("input = %q, want png", runner.input)
	}
	call := runner.calls[0]
	if call.name != "ssh" || !containsArgs(call.args, "-F", "/tmp/ssh config") {
		t.Fatalf("ssh call = %#v", call)
	}
	script := call.args[len(call.args)-1]
	if !strings.Contains(script, "umask 077") || !strings.Contains(script, "mktemp -d") || !strings.Contains(script, "nexo-paste-XXXXXXXX") {
		t.Fatalf("upload script = %q, want private directory strategy", script)
	}
	if strings.Contains(script, "nexo-paste-XXXXXXXX.png") {
		t.Fatalf("upload script = %q, must not rely on suffix-after-X mktemp semantics", script)
	}
}

// TestUploadCommandCreatesPrivateDirectoryAndFile runs the portable upload
// shell on the host so the same test exercises GNU and BSD mktemp semantics.
func TestUploadCommandCreatesPrivateDirectoryAndFile(t *testing.T) {
	parent := t.TempDir()
	tmp := filepath.Join(parent, "tmp dir with spaces")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	command := exec.Command("sh", "-c", uploadCommand)
	command.Stdin = strings.NewReader("png")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("upload command error = %v", err)
	}
	path := strings.TrimSuffix(string(output), "\n")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat upload = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("upload mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat upload dir = %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("upload dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "nexo-paste-") || !strings.HasSuffix(base, ".png") {
		t.Fatalf("upload base = %q", base)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "png" {
		t.Fatalf("upload content = %q, %v", content, err)
	}
}

// TestUploadCommandRunsUnderZsh guards the remote login shell: zsh ties
// $path to $PATH, so a variable with that name breaks every later command.
func TestUploadCommandRunsUnderZsh(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh is not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())
	command := exec.Command("zsh", "-c", uploadCommand)
	command.Stdin = strings.NewReader("png")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("upload command under zsh error = %v, output %q", err, output)
	}
	content, err := os.ReadFile(strings.TrimSuffix(string(output), "\n"))
	if err != nil || string(content) != "png" {
		t.Fatalf("upload content = %q, %v", content, err)
	}
}

func TestUploadPNGRejectsUnsafeRemotePaths(t *testing.T) {
	cases := []struct {
		name   string
		output string
	}{
		{name: "wrong basename", output: "/tmp/not-safe\n"},
		{name: "relative", output: "tmp/nexo-paste-a.png\n"},
		{name: "escape byte", output: "/tmp/nexo-paste-\x1b.png\n"},
		{name: "carriage return", output: "/tmp/nexo-paste-a.png\r\n"},
		{name: "embedded newline", output: "/tmp/nexo-paste-a.png\n/tmp/nexo-paste-b.png\n"},
		{name: "empty", output: "\n"},
		{name: "directory only", output: "/tmp/nexo-paste-a/\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := &pasteRunner{output: []byte(test.output)}
			if _, err := UploadPNG(context.Background(), runner, "", "devbox", []byte("png")); err == nil {
				t.Fatalf("UploadPNG(%q) error = nil, want rejection", test.output)
			}
		})
	}
}

func TestUploadPNGAcceptsPathsNeedingQuoting(t *testing.T) {
	for _, path := range []string{
		"/tmp/nexo paste/nexo-paste-a.png",
		"/tmp/$(evil)/nexo-paste-a.png",
		"/tmp/`evil`/nexo-paste-a.png",
		"/tmp/nexo-paste-a'; echo pwned; .png",
	} {
		runner := &pasteRunner{output: []byte(path + "\n")}
		got, err := UploadPNG(context.Background(), runner, "", "devbox", []byte("png"))
		if err != nil || got != path {
			t.Fatalf("UploadPNG(%q) = %q, %v; want raw path", path, got, err)
		}
	}
}

func TestShellQuotePath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "/tmp/nexo-paste-a/nexo-paste-image.png", want: "/tmp/nexo-paste-a/nexo-paste-image.png"},
		{path: "/tmp/nexo paste/nexo-paste-image.png", want: "'/tmp/nexo paste/nexo-paste-image.png'"},
		{path: "/tmp/$(evil)/nexo-paste-image.png", want: "'/tmp/$(evil)/nexo-paste-image.png'"},
		{path: "/tmp/`evil`/nexo-paste-image.png", want: "'/tmp/`evil`/nexo-paste-image.png'"},
		{path: "/tmp/nexo-paste-a'b/nexo-paste-image.png", want: "'/tmp/nexo-paste-a'\\''b/nexo-paste-image.png'"},
	}
	for _, test := range cases {
		if got := ShellQuotePath(test.path); got != test.want {
			t.Fatalf("ShellQuotePath(%q) = %q, want %q", test.path, got, test.want)
		}
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

package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

type fakeRunner struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if err := f.errors[call]; err != nil {
		return nil, err
	}
	return f.outputs[call], nil
}

func TestDetectsGoInstallFromGOBIN(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{
		"uname -s":              []byte("Linux\n"),
		"uname -m":              []byte("x86_64\n"),
		"sh -c command -v nexo": []byte("/home/user/go/bin/nexo\n"),
		"brew --prefix nexo":    nil,
		"go env GOBIN":          []byte("/home/user/go/bin\n"),
		"go env GOPATH":         []byte("/home/user/go\n"),
	}}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodGo || got.Path != "/home/user/go/bin/nexo" || got.Platform.String() != "linux/amd64" {
		t.Fatalf("installation = %#v", got)
	}
}

func TestDetectsHomebrewBeforeGo(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{
		"uname -s":              []byte("Darwin\n"),
		"uname -m":              []byte("arm64\n"),
		"sh -c command -v nexo": []byte("/opt/homebrew/bin/nexo\n"),
		"brew --prefix nexo":    []byte("/opt/homebrew/opt/nexo\n"),
	}}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodHomebrew || got.Path != "/opt/homebrew/opt/nexo/bin/nexo" {
		t.Fatalf("installation = %#v", got)
	}
}

func TestDetectsPlainBinaryWhenGoPathDoesNotMatch(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{
		"uname -s":              []byte("Linux\n"),
		"uname -m":              []byte("aarch64\n"),
		"sh -c command -v nexo": []byte("/usr/local/bin/nexo\n"),
		"brew --prefix nexo":    nil,
		"go env GOBIN":          []byte("\n"),
		"go env GOPATH":         []byte("/home/user/go\n"),
	}}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodBinary {
		t.Fatalf("method = %q, want %q", got.Method, MethodBinary)
	}
}

func TestReadVersionParsesNexoOutput(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{
		"/usr/local/bin/nexo --version": []byte("nexo v1.2.3\n"),
	}}
	got, err := ReadVersion(context.Background(), NewLocalTarget(fake), "/usr/local/bin/nexo")
	if err != nil || got != "v1.2.3" {
		t.Fatalf("ReadVersion() = %q, %v", got, err)
	}
}

func TestCopyAtomicReplacesLocalFile(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "built-nexo")
	destination := filepath.Join(directory, "bin", "nexo")
	contents := []byte("new binary")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyAtomic(context.Background(), NewLocalTarget(runner.ExecRunner{}), source, destination); err != nil {
		t.Fatalf("CopyAtomic() error = %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("destination = %q, want %q", got, contents)
	}
	if mode := mustStat(t, destination).Mode().Perm(); mode != 0o755 {
		t.Fatalf("destination mode = %o, want 755", mode)
	}
}

func TestRemoteTargetAddsSSHAlias(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{
		"ssh -o BatchMode=yes -F /tmp/ssh-config devbox 'uname' '-s'": []byte("Linux\n"),
	}}
	_, err := NewRemoteTarget(fake, "devbox", "/tmp/ssh-config").run(context.Background(), "uname", "-s")
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if len(fake.calls) != 1 || !strings.Contains(fake.calls[0], "devbox") {
		t.Fatalf("calls = %#v", fake.calls)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestDetectReportsMissingPath(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{
		"uname -s":              []byte("Linux\n"),
		"uname -m":              []byte("x86_64\n"),
		"sh -c command -v nexo": nil,
		"brew --prefix nexo":    nil,
		"go env GOBIN":          nil,
		"go env GOPATH":         nil,
	}, errors: map[string]error{
		"sh -c command -v nexo": errors.New("not found"),
		"brew --prefix nexo":    errors.New("not found"),
		"go env GOBIN":          errors.New("not found"),
		"go env GOPATH":         errors.New("not found"),
	}}
	if _, err := Detect(context.Background(), NewLocalTarget(fake)); err == nil || !strings.Contains(err.Error(), "find nexo on PATH") {
		t.Fatalf("Detect() error = %v, want missing-installation error", err)
	}
}

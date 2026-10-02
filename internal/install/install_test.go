package install

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vieitesss/nexo/internal/runner"
)

type fakeRunner struct {
	run   func(name string, args ...string) ([]byte, error)
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	if f.run == nil {
		return nil, nil
	}
	return f.run(name, args...)
}

func (f *fakeRunner) RunInput(_ context.Context, input []byte, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	if f.run == nil {
		return nil, nil
	}
	return f.run(name, args...)
}

func platformRunner(extra func(name string, args ...string) ([]byte, error)) func(string, ...string) ([]byte, error) {
	return func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "uname -s":
			return []byte("Linux\n"), nil
		case "uname -m":
			return []byte("x86_64\n"), nil
		}
		if extra != nil {
			return extra(name, args...)
		}
		return nil, errors.New("unexpected command")
	}
}

func TestProbeReportsPlatformWithoutNexo(t *testing.T) {
	fake := &fakeRunner{run: platformRunner(nil)}
	platform, err := Probe(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if platform.String() != "linux/amd64" {
		t.Fatalf("platform = %q, want linux/amd64", platform)
	}
}

func TestDetectsHomebrewWhenKegOwnsExecutable(t *testing.T) {
	root := t.TempDir()
	keg := filepath.Join(root, "Cellar", "nexo", "1.2.3")
	bin := filepath.Join(keg, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "nexo"), []byte("brew"), 0o755); err != nil {
		t.Fatal(err)
	}
	opt := filepath.Join(root, "opt", "nexo")
	if err := os.MkdirAll(filepath.Dir(opt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keg, opt); err != nil {
		t.Fatal(err)
	}
	brewBin := filepath.Join(root, "homebrew", "bin", "nexo")
	if err := os.MkdirAll(filepath.Dir(brewBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(opt, "bin", "nexo"), brewBin); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return []byte(brewBin + "\n"), nil
		case "brew --prefix nexo":
			return []byte(opt + "\n"), nil
		}
		return nil, errors.New("unexpected command")
	})}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodHomebrew || got.Path != brewBin || got.RealPath != filepath.Join(bin, "nexo") {
		t.Fatalf("installation = %#v", got)
	}
}

func TestDetectsPathBinaryWhenHomebrewAlsoInstalled(t *testing.T) {
	root := t.TempDir()
	opt := filepath.Join(root, "opt", "nexo")
	if err := os.MkdirAll(filepath.Join(opt, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opt, "bin", "nexo"), []byte("brew"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "usr", "local", "bin", "nexo")
	if err := os.MkdirAll(filepath.Dir(plain), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return []byte(plain + "\n"), nil
		case "brew --prefix nexo":
			return []byte(opt + "\n"), nil
		}
		return nil, errors.New("unexpected command")
	})}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodBinary || got.Path != plain {
		t.Fatalf("installation = %#v, want the PATH binary", got)
	}
}

func TestDetectsHomebrewWhenPathIsEmpty(t *testing.T) {
	root := t.TempDir()
	opt := filepath.Join(root, "opt", "nexo")
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return nil, errors.New("not found")
		case "brew --prefix nexo":
			return []byte(opt + "\n"), nil
		}
		return nil, errors.New("unexpected command")
	})}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodHomebrew || got.Path != filepath.Join(opt, "bin", "nexo") {
		t.Fatalf("installation = %#v", got)
	}
}

func TestDetectsExplicitLocalInvocation(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "usr", "local", "bin", "nexo")
	if err := os.MkdirAll(filepath.Dir(explicit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, []byte("explicit"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, ".local", "bin", "nexo")
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return []byte(other + "\n"), nil
		case "brew --prefix nexo":
			return nil, errors.New("brew not installed")
		}
		return nil, errors.New("unexpected command")
	})}
	target := NewLocalTarget(fake)
	target.Launch = explicit
	got, err := Detect(context.Background(), target)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Path != explicit {
		t.Fatalf("path = %q, want the explicit invocation %q", got.Path, explicit)
	}
}

func TestDetectsGoInstallThroughSymlink(t *testing.T) {
	root := t.TempDir()
	gobin := filepath.Join(root, "go", "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	referent := filepath.Join(gobin, "nexo")
	if err := os.WriteFile(referent, []byte("go"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".local", "bin", "nexo")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(referent, link); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return []byte(link + "\n"), nil
		case "brew --prefix nexo":
			return nil, errors.New("brew not installed")
		case "go env GOBIN":
			return []byte(gobin + "\n"), nil
		case "go env GOPATH":
			return []byte(filepath.Join(root, "go") + "\n"), nil
		}
		return nil, errors.New("unexpected command")
	})}
	got, err := Detect(context.Background(), NewLocalTarget(fake))
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Method != MethodGo || got.Path != link || got.RealPath != referent {
		t.Fatalf("installation = %#v", got)
	}
}

func TestRemoteSSHUsesBoundedConnection(t *testing.T) {
	target := NewRemoteTarget(&fakeRunner{}, "devbox", "/tmp/ssh-config")
	joined := strings.Join(target.sshArgs("true"), " ")
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=5", "-F /tmp/ssh-config", "devbox"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args = %q, want %q", joined, want)
		}
	}
}

func TestReadVersionParsesNexoOutput(t *testing.T) {
	fake := &fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		return []byte("nexo v1.2.3\n"), nil
	}}
	got, err := ReadVersion(context.Background(), NewLocalTarget(fake), "/usr/local/bin/nexo")
	if err != nil || got != "v1.2.3" {
		t.Fatalf("ReadVersion() = %q, %v", got, err)
	}
}

func TestRunIncludesExitErrorStderr(t *testing.T) {
	target := NewLocalTarget(runner.ExecRunner{})
	_, err := target.run(context.Background(), "sh", "-c", "echo 'fatal: authentication failed' >&2; exit 1")
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("run() error = %v, want stderr detail", err)
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
	published := false
	if err := CopyAtomic(context.Background(), NewLocalTarget(runner.ExecRunner{}), source, destination, func() {
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("publish callback observed destination error = %v, want not-exist", err)
		}
		published = true
	}); err != nil {
		t.Fatalf("CopyAtomic() error = %v", err)
	}
	if !published {
		t.Fatal("publish callback was not called")
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

func TestCopyAtomicRemotePublishesAfterTransferCallback(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "built-nexo")
	if err := os.WriteFile(source, []byte("new binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	var actions []string
	remote := &fakeRunner{}
	// fakeRunner's RunInput delegates to run; return the transfer staging path
	// from that callback while recording the publish command in Run.
	remote.run = func(name string, args ...string) ([]byte, error) {
		if name == "ssh" {
			command := strings.Join(args, " ")
			actions = append(actions, command)
			if strings.Contains(command, "sh -c") {
				return []byte("/tmp/nexo-stage\n"), nil
			}
		}
		return nil, nil
	}
	published := false
	if err := CopyAtomic(context.Background(), NewRemoteTarget(remote, "test", ""), source, "/usr/local/bin/nexo", func() {
		published = true
	}); err != nil {
		t.Fatalf("CopyAtomic() error = %v", err)
	}
	if !published || len(actions) < 2 {
		t.Fatalf("actions = %#v, callback = %v", actions, published)
	}
	if !strings.Contains(actions[0], "'sh'") || !strings.Contains(actions[1], "'mv'") {
		t.Fatalf("actions = %#v, want transfer then publish", actions)
	}
}

// publishingCopyScript runs the transfer receiver followed by the publish
// rename, as CopyAtomic does over two remote actions.
func publishingCopyScript(destination string, size int, digest string) string {
	return copyTransferScript(destination, destination+".nexo-copy.test.tmp", size, digest) + "; mv -f \"$tmp\" \"$dest\""
}

func TestCopyScriptRejectsBrokenTransfers(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "bin", "nexo")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("complete-new-binary")
	sum := sha256.Sum256(contents)
	script := publishingCopyScript(destination, len(contents), hex.EncodeToString(sum[:]))
	run := func(input []byte) error {
		command := exec.Command("sh", "-c", script)
		command.Stdin = bytes.NewReader(input)
		return command.Run()
	}
	for _, input := range [][]byte{nil, []byte("partial")} {
		if err := run(input); err == nil {
			t.Fatalf("transfer of %d bytes succeeded, want truncation failure", len(input))
		}
		if got, err := os.ReadFile(destination); err != nil || string(got) != "old" {
			t.Fatalf("destination changed to %q (%v), want old", got, err)
		}
	}
	if err := run(contents); err != nil {
		t.Fatalf("complete transfer failed: %v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != string(contents) {
		t.Fatalf("destination = %q (%v)", got, err)
	}
	if mode := mustStat(t, destination).Mode().Perm(); mode != 0o755 {
		t.Fatalf("destination mode = %o, want 755", mode)
	}
}

func TestCopyScriptLeavesDestinationWhenDirectoryIsUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	directory := t.TempDir()
	locked := filepath.Join(directory, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(locked, "nexo")
	if err := os.WriteFile(destination, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	contents := []byte("complete-new-binary")
	sum := sha256.Sum256(contents)
	command := exec.Command("sh", "-c", publishingCopyScript(destination, len(contents), hex.EncodeToString(sum[:])))
	command.Stdin = bytes.NewReader(contents)
	if err := command.Run(); err == nil {
		t.Fatal("unwritable destination directory accepted the copy")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "old" {
		t.Fatalf("destination = %q (%v), want old", got, err)
	}
}

func TestLocalBuilderCrossBuildsWithoutGOBIN(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available")
	}
	work := t.TempDir()
	proxy := filepath.Join(work, "proxy")
	modulePath := "example.com/nexotest"
	writeModuleProxy(t, proxy, modulePath, "v0.0.0")
	modcache := writableTempDir(t)
	binDir := filepath.Join(work, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(binDir, "go")
	script := "#!/bin/sh\nunset GOPRIVATE GONOPROXY GONOSUMDB\n" +
		"export GOPROXY=file://" + proxy + "\nexport GOSUMDB=off\nexport GOFLAGS=\n" +
		"export GOTOOLCHAIN=local\nexport GOMODCACHE=" + modcache + "\n" +
		"exec " + realGo + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	destination := filepath.Join(work, "staged-nexo")
	builder := &LocalBuilder{Runner: runner.ExecRunner{}}
	if err := builder.Build(context.Background(), modulePath, "v0.0.0", Platform{OS: "darwin", Arch: "arm64"}, destination); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 4 || !bytes.Equal(contents[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) {
		t.Fatalf("destination is not a darwin/arm64 Mach-O: % x", contents)
	}
}

func TestInstallRejectsHomebrewBeforeMutation(t *testing.T) {
	root := t.TempDir()
	opt := filepath.Join(root, "opt", "nexo")
	if err := os.MkdirAll(filepath.Join(opt, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opt, "bin", "nexo"), []byte("brew"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return []byte(filepath.Join(opt, "bin", "nexo") + "\n"), nil
		case "brew --prefix nexo":
			return []byte(opt + "\n"), nil
		}
		return nil, errors.New("unexpected command")
	})}
	_, err := Install(context.Background(), NewLocalTarget(fake), Request{Ref: "v1.2.3", Local: &LocalBuilder{Runner: fake}})
	if err == nil || !strings.Contains(err.Error(), "brew upgrade") {
		t.Fatalf("Install() error = %v, want Homebrew guidance", err)
	}
	for _, call := range fake.calls {
		if strings.Contains(call, "go install") || strings.Contains(call, "go version") {
			t.Fatalf("Install() probed or built before failing: %q", call)
		}
	}
}

func TestInstallPublishesReferentAndPreservesSymlink(t *testing.T) {
	root := t.TempDir()
	gobin := filepath.Join(root, "go", "bin")
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		t.Fatal(err)
	}
	referent := filepath.Join(gobin, "nexo")
	if err := os.WriteFile(referent, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".local", "bin", "nexo")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(referent, link); err != nil {
		t.Fatal(err)
	}
	destPattern := regexp.MustCompile(`dest='([^']+)'`)
	var events []ProgressStep
	fake := &fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		switch {
		case key == "uname -s":
			return []byte("Linux\n"), nil
		case key == "uname -m":
			return []byte("x86_64\n"), nil
		case key == "sh -c command -v nexo":
			return []byte(link + "\n"), nil
		case key == "brew --prefix nexo":
			return nil, errors.New("brew not installed")
		case key == "go env GOBIN":
			return []byte(gobin + "\n"), nil
		case key == "go env GOPATH":
			return []byte(filepath.Join(root, "go") + "\n"), nil
		case key == "go version":
			return []byte("go version go1.27.1\n"), nil
		case name == "sh" && len(args) == 2 && args[0] == "-c":
			match := destPattern.FindStringSubmatch(args[1])
			if match == nil {
				return nil, errors.New("build script has no destination")
			}
			return nil, os.WriteFile(match[1], []byte("new-binary"), 0o755)
		case strings.HasSuffix(key, " --version"):
			return []byte("nexo v0.0.0-20200101000000-abcdef123456\n"), nil
		}
		return nil, errors.New("unexpected command: " + key)
	}}
	got, err := Install(context.Background(), NewLocalTarget(fake), Request{
		Ref: "abcdef1234567890", Local: &LocalBuilder{Runner: fake},
		Progress: func(step ProgressStep) { events = append(events, step) },
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if got.Path != link || got.RealPath != referent {
		t.Fatalf("installation = %#v", got)
	}
	if got.Version != "v0.0.0-20200101000000-abcdef123456" {
		t.Fatalf("version = %q", got.Version)
	}
	wantEvents := []ProgressStep{ProgressBuilding, ProgressCopying, ProgressInstalling}
	if len(events) != len(wantEvents) {
		t.Fatalf("progress events = %#v, want %#v", events, wantEvents)
	}
	for index, want := range wantEvents {
		if events[index] != want {
			t.Fatalf("progress event %d = %q, want %q", index, events[index], want)
		}
	}
	linkTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("launch symlink was replaced: %v", err)
	}
	if linkTarget != referent {
		t.Fatalf("launch symlink points to %q, want %q", linkTarget, referent)
	}
	if contents, err := os.ReadFile(referent); err != nil || string(contents) != "new-binary" {
		t.Fatalf("referent = %q (%v), want new-binary", contents, err)
	}
}

func TestDetectReportsMissingPath(t *testing.T) {
	fake := &fakeRunner{run: platformRunner(func(name string, args ...string) ([]byte, error) {
		switch strings.Join(append([]string{name}, args...), " ") {
		case "sh -c command -v nexo":
			return nil, errors.New("not found")
		case "brew --prefix nexo":
			return nil, errors.New("not found")
		}
		return nil, errors.New("unexpected command")
	})}
	if _, err := Detect(context.Background(), NewLocalTarget(fake)); err == nil || !strings.Contains(err.Error(), "find nexo on PATH") {
		t.Fatalf("Detect() error = %v, want missing-installation error", err)
	}
}

func writeModuleProxy(t *testing.T, root, modulePath, version string) {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(modulePath), "@v")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(base, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("list", version+"\n")
	write(version+".info", `{"Version":"`+version+`","Time":"2020-01-01T00:00:00Z"}`)
	write(version+".mod", "module "+modulePath+"\n\ngo 1.21\n")
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	add := func(name, contents string) {
		writer, err := archive.Create(modulePath + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	add("go.mod", "module "+modulePath+"\n\ngo 1.21\n")
	add("cmd/nexo/main.go", "package main\n\nfunc main() {}\n")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, version+".zip"), buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writableTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	return directory
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

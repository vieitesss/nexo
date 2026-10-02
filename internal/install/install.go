// Package install detects nexo installations and replaces them atomically.
//
// Target is the small seam shared by local commands and SSH-backed remote
// machines. The installation policy remains here so setup and upgrade cannot
// drift apart.
package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vieitesss/nexo/internal/runner"
)

const defaultRepository = "github.com/vieitesss/nexo"

// Method identifies how a nexo binary was installed.
type Method string

const (
	MethodHomebrew Method = "homebrew"
	MethodGo       Method = "go-install"
	MethodBinary   Method = "binary"
)

// Platform is the target operating system and architecture.
type Platform struct {
	OS   string
	Arch string
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// Target describes a local machine or an SSH alias. A target's Runner is the
// existing process runner; the package only adds SSH argument construction for
// remote targets.
type Target struct {
	Runner      runner.CommandRunner
	InputRunner runner.InputRunner
	Machine     string
	SSHConfig   string
}

// NewLocalTarget creates a target that runs commands directly.
func NewLocalTarget(commandRunner runner.CommandRunner) Target {
	return Target{Runner: commandRunner, InputRunner: inputRunner(commandRunner)}
}

// NewRemoteTarget creates a target that runs commands through SSH.
func NewRemoteTarget(commandRunner runner.CommandRunner, machine, sshConfig string) Target {
	return Target{
		Runner:      commandRunner,
		InputRunner: inputRunner(commandRunner),
		Machine:     strings.TrimSpace(machine),
		SSHConfig:   strings.TrimSpace(sshConfig),
	}
}

func inputRunner(commandRunner runner.CommandRunner) runner.InputRunner {
	input, _ := commandRunner.(runner.InputRunner)
	return input
}

// IsRemote reports whether commands are sent to an SSH alias.
func (t Target) IsRemote() bool { return strings.TrimSpace(t.Machine) != "" }

func (t Target) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if t.Runner == nil {
		return nil, errors.New("install target requires a command runner")
	}
	if !t.IsRemote() {
		return t.Runner.Run(ctx, name, args...)
	}
	return t.Runner.Run(ctx, "ssh", t.sshArgs(remoteCommand(name, args...))...)
}

func (t Target) runInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	if t.InputRunner == nil {
		return nil, errors.New("install target requires an input-capable command runner")
	}
	if !t.IsRemote() {
		return t.InputRunner.RunInput(ctx, input, name, args...)
	}
	return t.InputRunner.RunInput(ctx, input, "ssh", t.sshArgs(remoteCommand(name, args...))...)
}

func (t Target) sshArgs(command string) []string {
	args := []string{"-o", "BatchMode=yes"}
	if t.SSHConfig != "" {
		args = append(args, "-F", t.SSHConfig)
	}
	return append(args, t.Machine, command)
}

func remoteCommand(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(name))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Installation describes the currently resolved installation on one target.
type Installation struct {
	Method   Method
	Path     string
	Platform Platform
	Version  string
}

// Request identifies the source to install. Version is a release tag when
// Nightly is false; Nightly installs the main branch.
type Request struct {
	Version    string
	Nightly    bool
	Repository string
}

func (r Request) repository() string {
	if strings.TrimSpace(r.Repository) == "" {
		return defaultRepository
	}
	return strings.TrimSpace(r.Repository)
}

// Detect finds the platform, installation method, and executable path on a
// target. Homebrew, Go installs, and release binaries are considered in that
// order, matching the supported installation methods.
func Detect(ctx context.Context, target Target) (Installation, error) {
	platform, err := detectPlatform(ctx, target)
	if err != nil {
		return Installation{}, err
	}
	path, pathErr := commandPath(ctx, target)
	brewPrefix, brewOK := homebrewPrefix(ctx, target)
	if brewOK {
		// Use the formula's real executable rather than the Homebrew shim on
		// PATH. A nightly fallback can then replace it without breaking that
		// shim, and subsequent Homebrew upgrades keep owning the symlink.
		return Installation{Method: MethodHomebrew, Path: filepath.Join(brewPrefix, "bin", "nexo"), Platform: platform}, nil
	}
	if pathErr != nil {
		return Installation{}, pathErr
	}
	if path == "" {
		return Installation{}, errors.New("nexo is not installed (command -v nexo returned no path)")
	}
	if goInstallPath(ctx, target, path) {
		return Installation{Method: MethodGo, Path: path, Platform: platform}, nil
	}
	return Installation{Method: MethodBinary, Path: path, Platform: platform}, nil
}

func detectPlatform(ctx context.Context, target Target) (Platform, error) {
	osName, err := target.run(ctx, "uname", "-s")
	if err != nil {
		return Platform{}, fmt.Errorf("detect operating system: %w", err)
	}
	archName, err := target.run(ctx, "uname", "-m")
	if err != nil {
		return Platform{}, fmt.Errorf("detect architecture: %w", err)
	}
	platform := Platform{OS: normalizeOS(string(osName)), Arch: normalizeArch(string(archName))}
	if platform.OS == "" || platform.Arch == "" {
		return Platform{}, fmt.Errorf("unsupported platform %q/%q", strings.TrimSpace(string(osName)), strings.TrimSpace(string(archName)))
	}
	return platform, nil
}

func normalizeOS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "darwin", "macos":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return ""
	}
}

func normalizeArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func commandPath(ctx context.Context, target Target) (string, error) {
	output, err := target.run(ctx, "sh", "-c", "command -v nexo")
	if err != nil {
		return "", fmt.Errorf("find nexo on PATH: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func homebrewPrefix(ctx context.Context, target Target) (string, bool) {
	output, err := target.run(ctx, "brew", "--prefix", "nexo")
	if err != nil {
		return "", false
	}
	prefix := strings.TrimSpace(string(output))
	return prefix, prefix != ""
}

func goInstallPath(ctx context.Context, target Target, path string) bool {
	gobinOutput, gobinErr := target.run(ctx, "go", "env", "GOBIN")
	gopathOutput, gopathErr := target.run(ctx, "go", "env", "GOPATH")
	if gobinErr != nil && gopathErr != nil {
		return false
	}
	path = filepath.Clean(path)
	if gobin := strings.TrimSpace(string(gobinOutput)); gobin != "" && path == filepath.Join(gobin, "nexo") {
		return true
	}
	for _, gopath := range filepath.SplitList(strings.TrimSpace(string(gopathOutput))) {
		if gopath != "" && path == filepath.Join(gopath, "bin", "nexo") {
			return true
		}
	}
	return false
}

// ReadVersion runs nexo --version at path and returns its version token.
func ReadVersion(ctx context.Context, target Target, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("cannot read nexo version without an executable path")
	}
	output, err := target.run(ctx, path, "--version")
	if err != nil {
		return "", fmt.Errorf("read nexo version: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "nexo" {
			return fields[1], nil
		}
		return line, nil
	}
	return "", errors.New("nexo --version returned no version")
}

// Install detects the existing installation, installs the requested source
// using its method, and returns the refreshed installation metadata. Every
// source-build and binary replacement writes a temporary file beside the old
// path before renaming it.
func Install(ctx context.Context, target Target, request Request) (Installation, error) {
	current, err := Detect(ctx, target)
	if err != nil {
		return Installation{}, err
	}
	if request.Nightly && current.Method == MethodHomebrew {
		return Installation{}, errors.New("homebrew cannot install --nightly; install Go or use a release binary")
	}
	if !request.Nightly && strings.TrimSpace(request.Version) == "" {
		return Installation{}, errors.New("a release version is required")
	}
	switch current.Method {
	case MethodHomebrew:
		err = installHomebrew(ctx, target)
	case MethodGo:
		err = installGo(ctx, target, current.Path, request)
	case MethodBinary:
		err = installReleaseBinary(ctx, target, current, request)
	default:
		err = fmt.Errorf("unsupported install method %q", current.Method)
	}
	if err != nil {
		return Installation{}, err
	}
	updated, err := Detect(ctx, target)
	if err != nil {
		return Installation{}, err
	}
	updated.Version, err = ReadVersion(ctx, target, updated.Path)
	if err != nil {
		return Installation{}, err
	}
	return updated, nil
}

func installHomebrew(ctx context.Context, target Target) error {
	if _, err := target.run(ctx, "brew", "upgrade", "vieitesss/tap/nexo"); err != nil {
		return fmt.Errorf("upgrade nexo with Homebrew: %w", err)
	}
	return nil
}

func installGo(ctx context.Context, target Target, path string, request Request) error {
	ref := request.Version
	if request.Nightly {
		ref = "main"
	}
	directory := filepath.Dir(path)
	script := fmt.Sprintf("set -eu; dir=%s; tmp=$(mktemp -d \"$dir/.nexo-install.XXXXXX\"); trap 'rm -rf \"$tmp\"' EXIT; env GOPRIVATE=%s GOBIN=\"$tmp\" go install %s/cmd/nexo@%s; chmod 0755 \"$tmp/nexo\"; mv -f \"$tmp/nexo\" %s", shellQuote(directory), shellQuote(request.repository()), shellQuote(request.repository()), shellQuote(ref), shellQuote(path))
	if _, err := target.run(ctx, "sh", "-c", script); err != nil {
		return fmt.Errorf("install nexo with Go: %w", err)
	}
	return nil
}

func installReleaseBinary(ctx context.Context, target Target, current Installation, request Request) error {
	if request.Nightly {
		return errors.New("nightly requires Go or a locally built binary")
	}
	archive := fmt.Sprintf("nexo_*_%s_%s.tar.gz", current.Platform.OS, current.Platform.Arch)
	directory := filepath.Dir(current.Path)
	script := fmt.Sprintf("set -eu; dir=%s; tmp=$(mktemp -d \"$dir/.nexo-install.XXXXXX\"); trap 'rm -rf \"$tmp\"' EXIT; gh release download %s --repo %s --pattern %s --dir \"$tmp\"; archive=$(find \"$tmp\" -maxdepth 1 -name %s -print -quit); test -n \"$archive\"; tar -xzf \"$archive\" -C \"$tmp\"; chmod 0755 \"$tmp/nexo\"; mv -f \"$tmp/nexo\" %s", shellQuote(directory), shellQuote(request.Version), shellQuote(strings.TrimPrefix(request.repository(), "github.com/")), shellQuote(archive), shellQuote(archive), shellQuote(current.Path))
	if _, err := target.run(ctx, "sh", "-c", script); err != nil {
		return fmt.Errorf("install nexo release: %w", err)
	}
	return nil
}

// CopyAtomic replaces destination with source on target. It is used when a
// target has no Go toolchain: the caller builds for target.Platform locally,
// then this method transfers the result and renames it remotely in one
// directory.
func CopyAtomic(ctx context.Context, target Target, source, destination string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read built nexo binary: %w", err)
	}
	if !target.IsRemote() {
		return writeAtomic(destination, contents)
	}
	script := fmt.Sprintf("set -eu; dir=$(dirname %s); tmp=$(mktemp \"$dir/.nexo-copy.XXXXXX\"); trap 'rm -f \"$tmp\"' EXIT; cat >\"$tmp\"; chmod 0755 \"$tmp\"; mv -f \"$tmp\" %s", shellQuote(destination), shellQuote(destination))
	if _, err := target.runInput(ctx, contents, "sh", "-c", script); err != nil {
		return fmt.Errorf("copy nexo binary to %s: %w", target.Machine, err)
	}
	return nil
}

func writeAtomic(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".nexo-copy-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o755); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(contents)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// LocalPlatform is useful to callers that need to cross-compile a fallback
// build without probing another target.
func LocalPlatform() Platform { return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH} }

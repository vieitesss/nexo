// Package install detects nexo installations and replaces them atomically.
//
// Target is the small seam shared by local commands and SSH-backed remote
// machines. The installation policy remains here so setup and upgrade cannot
// drift apart.
package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

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
	// Launch overrides the local executable path used for detection. Upgrade
	// sets it to the path this process was invoked as; when empty, Detect
	// reads os.Args[0] for the local machine. It is ignored for remotes.
	Launch string
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
		output, err := t.Runner.Run(ctx, name, args...)
		return output, runner.Detail(err)
	}
	output, err := t.Runner.Run(ctx, "ssh", t.sshArgs(remoteCommand(name, args...))...)
	return output, runner.Detail(err)
}

func (t Target) runInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	if t.InputRunner == nil {
		return nil, errors.New("install target requires an input-capable command runner")
	}
	if !t.IsRemote() {
		output, err := t.InputRunner.RunInput(ctx, input, name, args...)
		return output, runner.Detail(err)
	}
	output, err := t.InputRunner.RunInput(ctx, input, "ssh", t.sshArgs(remoteCommand(name, args...))...)
	return output, runner.Detail(err)
}

func (t Target) sshArgs(command string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
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
	Method Method
	// Path is the launch path: the command on PATH, or the local invocation
	// path. It may be a symlink and is what reports and version checks use.
	Path string
	// RealPath is the resolved executable referent. Publication renames
	// beside and over this file so a launch symlink keeps working.
	RealPath string
	Platform Platform
	Version  string
}

// ProgressStep identifies a real phase of an installation.
type ProgressStep string

const (
	ProgressBuilding   ProgressStep = "building"
	ProgressCopying    ProgressStep = "copying"
	ProgressInstalling ProgressStep = "installing"
)

// Request identifies the source to install. Ref is a release tag or the
// resolved commit of a nightly.
type Request struct {
	Ref        string
	Repository string
	// Local builds the ref on the command's machine when the target cannot.
	Local *LocalBuilder
	// Progress receives real installation phase transitions. It may be called
	// from a worker goroutine.
	Progress func(ProgressStep)
}

func (r Request) report(step ProgressStep) {
	if r.Progress != nil {
		r.Progress(step)
	}
}

func (r Request) repository() string {
	if strings.TrimSpace(r.Repository) == "" {
		return defaultRepository
	}
	return strings.TrimSpace(r.Repository)
}

// LocalBuilder builds an authenticated source revision on the machine that
// owns Runner.
type LocalBuilder struct {
	Runner runner.CommandRunner
}

// Available reports whether the local machine has a Go toolchain.
func (b *LocalBuilder) Available(ctx context.Context) bool {
	if b == nil || b.Runner == nil {
		return false
	}
	_, err := b.Runner.Run(ctx, "go", "version")
	return err == nil
}

// Build cross-compiles repository@ref for targetPlatform and writes the
// executable to destination. A temporary GOPATH holds the output because Go
// rejects a cross-installed command when GOBIN is set; the module cache is
// reused so the build stays offline once dependencies are cached.
func (b *LocalBuilder) Build(ctx context.Context, repository, ref string, targetPlatform Platform, destination string) error {
	if b == nil || b.Runner == nil {
		return errors.New("build requires a local command runner")
	}
	script := fmt.Sprintf(
		"set -eu; dest=%s; dir=$(dirname \"$dest\"); tmp=$(mktemp -d \"$dir/.nexo-build.XXXXXX\"); trap 'rm -rf \"$tmp\"' EXIT; modcache=$(go env GOMODCACHE); GOFLAGS= GOBIN= CGO_ENABLED=0 GOOS=%s GOARCH=%s GOPRIVATE=%s GOPATH=\"$tmp/gopath\" GOMODCACHE=\"$modcache\" go install %s/cmd/nexo@%s; built=\"$tmp/gopath/bin/%s_%s/nexo\"; [ -f \"$built\" ] || built=\"$tmp/gopath/bin/nexo\"; test -f \"$built\"; chmod 0755 \"$built\"; mv -f \"$built\" \"$dest\"",
		shellQuote(destination),
		shellQuote(targetPlatform.OS), shellQuote(targetPlatform.Arch), shellQuote(repository),
		shellQuote(repository), shellQuote(ref),
		targetPlatform.OS, targetPlatform.Arch,
	)
	if _, err := b.Runner.Run(ctx, "sh", "-c", script); err != nil {
		return fmt.Errorf("build nexo %s for %s: %w", ref, targetPlatform, runner.Detail(err))
	}
	return nil
}

// Probe reports a target's platform without requiring nexo to be installed.
func Probe(ctx context.Context, target Target) (Platform, error) {
	return detectPlatform(ctx, target)
}

// Detect finds the platform, installation method, executable path, and
// resolved referent on a target. The method follows the executable the user
// actually runs: a Homebrew install is recognized only when that executable
// belongs to the formula's keg, so another nexo earlier on PATH keeps its own
// method.
func Detect(ctx context.Context, target Target) (Installation, error) {
	platform, err := Probe(ctx, target)
	if err != nil {
		return Installation{}, err
	}
	launch, pathErr := commandPath(ctx, target)
	if !target.IsRemote() {
		if explicit := strings.TrimSpace(target.Launch); explicit != "" {
			launch, pathErr = explicit, nil
		} else if invoked := localInvocation(); invoked != "" {
			launch, pathErr = invoked, nil
		}
	}
	brewPrefix, brewOK := homebrewPrefix(ctx, target)
	if launch == "" && brewOK {
		path := filepath.Join(brewPrefix, "bin", "nexo")
		return Installation{Method: MethodHomebrew, Path: path, RealPath: resolvePath(ctx, target, path), Platform: platform}, nil
	}
	if launch == "" {
		if pathErr != nil {
			return Installation{}, pathErr
		}
		return Installation{}, errors.New("nexo is not installed (command -v nexo returned no path)")
	}
	real := resolvePath(ctx, target, launch)
	method := MethodBinary
	switch {
	case brewOK && homebrewOwns(ctx, target, real, brewPrefix):
		method = MethodHomebrew
	case looksHomebrew(real):
		method = MethodHomebrew
	case goInstallPath(ctx, target, launch, real):
		method = MethodGo
	}
	return Installation{Method: method, Path: launch, RealPath: real, Platform: platform}, nil
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

func homebrewOwns(ctx context.Context, target Target, real, prefix string) bool {
	resolved := resolvePath(ctx, target, prefix)
	if resolved == "" {
		return false
	}
	return real == resolved || strings.HasPrefix(real, resolved+string(filepath.Separator))
}

// looksHomebrew catches a Homebrew referent when brew itself is unavailable,
// so a keg is never mistaken for a plain binary and overwritten.
func looksHomebrew(path string) bool {
	return strings.Contains(path, "/Cellar/") || strings.Contains(path, "/homebrew/") || strings.Contains(path, "/linuxbrew/")
}

func goInstallPath(ctx context.Context, target Target, launch, real string) bool {
	gobinOutput, gobinErr := target.run(ctx, "go", "env", "GOBIN")
	gopathOutput, gopathErr := target.run(ctx, "go", "env", "GOPATH")
	if gobinErr != nil && gopathErr != nil {
		return false
	}
	candidates := []string{filepath.Clean(launch)}
	if real != "" && real != launch {
		candidates = append(candidates, filepath.Clean(real))
	}
	gobin := strings.TrimSpace(string(gobinOutput))
	for _, candidate := range candidates {
		if gobin != "" && candidate == filepath.Join(gobin, "nexo") {
			return true
		}
	}
	for _, gopath := range filepath.SplitList(strings.TrimSpace(string(gopathOutput))) {
		if gopath == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == filepath.Join(gopath, "bin", "nexo") {
				return true
			}
		}
	}
	return false
}

// resolvePath follows symlinks on the target, returning path unchanged when it
// cannot be resolved (for example a not-yet-installed destination).
func resolvePath(ctx context.Context, target Target, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !target.IsRemote() {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return path
	}
	script := fmt.Sprintf("p=%s; while [ -L \"$p\" ]; do t=$(readlink \"$p\"); case \"$t\" in /*) p=$t;; *) p=$(dirname \"$p\")/$t;; esac; done; printf '%%s' \"$p\"", shellQuote(path))
	output, err := target.run(ctx, "sh", "-c", script)
	if err != nil {
		return path
	}
	if resolved := strings.TrimSpace(string(output)); resolved != "" {
		return resolved
	}
	return path
}

// localInvocation returns the path this process was invoked as when it is a
// nexo executable outside a bare PATH lookup.
func localInvocation() string {
	invoked := os.Args[0]
	if invoked == "" || !strings.ContainsRune(invoked, os.PathSeparator) {
		return ""
	}
	executable, err := os.Executable()
	if err != nil || filepath.Base(executable) != "nexo" {
		return ""
	}
	absolute, err := filepath.Abs(invoked)
	if err != nil {
		return ""
	}
	return absolute
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

// Install builds the requested revision on the local machine and atomically
// publishes it at the existing installation's referent. The launch symlink, if
// any, is preserved.
func Install(ctx context.Context, target Target, request Request) (Installation, error) {
	current, err := Detect(ctx, target)
	if err != nil {
		return Installation{}, err
	}
	if current.Method == MethodHomebrew {
		return Installation{}, fmt.Errorf("homebrew owns %s and cannot be replaced atomically; run `brew upgrade vieitesss/tap/nexo` instead", current.Path)
	}
	ref := strings.TrimSpace(request.Ref)
	if ref == "" {
		return Installation{}, errors.New("install requires a release tag or commit")
	}
	if request.Local == nil || !request.Local.Available(ctx) {
		return Installation{}, errors.New("installing nexo requires a local Go toolchain with authenticated access to the source repository")
	}
	staged, err := os.CreateTemp("", "nexo-stage-*")
	if err != nil {
		return Installation{}, fmt.Errorf("create build staging file: %w", err)
	}
	stagedPath := staged.Name()
	if err := staged.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return Installation{}, fmt.Errorf("close build staging file: %w", err)
	}
	defer func() { _ = os.Remove(stagedPath) }()
	request.report(ProgressBuilding)
	if err := request.Local.Build(ctx, request.repository(), ref, current.Platform, stagedPath); err != nil {
		return Installation{}, err
	}
	request.report(ProgressCopying)
	if err := CopyAtomic(ctx, target, stagedPath, current.RealPath, func() {
		request.report(ProgressInstalling)
	}); err != nil {
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

// CopyAtomic replaces destination with source on target. It is used to publish
// a locally built binary: a replacement either lands whole or leaves the old
// executable untouched. beforePublish, when non-nil, runs once the replacement
// is staged and immediately before the atomic rename.
func CopyAtomic(ctx context.Context, target Target, source, destination string, beforePublish func()) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read built nexo binary: %w", err)
	}
	if !target.IsRemote() {
		return writeAtomic(destination, contents, beforePublish)
	}
	digest := sha256.Sum256(contents)
	// Transfer and publish are separate remote actions so the installing
	// event denotes the actual atomic rename rather than verification after it.
	temporary := fmt.Sprintf("%s.nexo-copy.%d.tmp", destination, time.Now().UnixNano())
	script := copyTransferScript(destination, temporary, len(contents), hex.EncodeToString(digest[:]))
	if _, err := target.runInput(ctx, contents, "sh", "-c", script); err != nil {
		return fmt.Errorf("copy nexo binary to %s: %w", target.Machine, err)
	}
	if beforePublish != nil {
		beforePublish()
	}
	if _, err := target.run(ctx, "mv", temporary, destination); err != nil {
		_, _ = target.run(context.WithoutCancel(ctx), "rm", "-f", temporary)
		return fmt.Errorf("install nexo binary on %s: %w", target.Machine, err)
	}
	return nil
}

// copyTransferScript is the remote receiver for CopyAtomic. It refuses to
// stage a truncated or altered transfer and leaves the staged file for the
// caller's publish action, so the existing executable stays intact when the
// connection drops.
func copyTransferScript(destination, temporary string, size int, digest string) string {
	count := strconv.Itoa(size)
	return fmt.Sprintf(
		"set -eu; dest=%s; tmp=%s; dir=$(dirname \"$dest\"); mkdir -p \"$dir\"; trap 'rm -f \"$tmp\"' EXIT; : >\"$tmp\"; cat >\"$tmp\"; size=$(wc -c <\"$tmp\" | tr -d ' '); [ \"$size\" = %s ] || { echo \"nexo: transfer truncated at $size of %s bytes\" >&2; exit 1; }; if command -v sha256sum >/dev/null 2>&1; then sum=$(sha256sum \"$tmp\" | cut -d' ' -f1); else sum=$(shasum -a 256 \"$tmp\" | cut -d' ' -f1); fi; [ \"$sum\" = %s ] || { echo \"nexo: transfer checksum mismatch\" >&2; exit 1; }; chmod 0755 \"$tmp\"; trap - EXIT",
		shellQuote(destination), shellQuote(temporary), shellQuote(count), shellQuote(count), shellQuote(digest),
	)
}

func writeAtomic(path string, contents []byte, beforePublish func()) error {
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
	if beforePublish != nil {
		beforePublish()
	}
	return os.Rename(temporary, path)
}

// LocalPlatform is the platform of the machine running this process.
func LocalPlatform() Platform { return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH} }

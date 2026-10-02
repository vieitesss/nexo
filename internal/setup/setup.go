package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/install"
	"github.com/vieitesss/nexo/internal/runner"
	"github.com/vieitesss/nexo/internal/tui"
)

// Options are the non-interactive setup controls. A zero value uses detected
// defaults; callers must set Yes or LocalOnly when stdin is not a TTY.
type Options struct {
	Backend      string
	ConfigPath   string
	SSHConfig    string
	Machines     []string
	LocalOnly    bool
	Yes          bool
	DryRun       bool
	Check        bool
	Paths        []string
	Notify       *bool
	RemoteConfig bool
}

// Input is the dependency seam for the setup workflow.
type Input struct {
	Runner          runner.CommandRunner
	Stdout          io.Writer
	Stderr          io.Writer
	Stdin           io.Reader
	Home            string
	TTY             bool
	SelectedBackend string
	ConfigPath      string
	LocalTmuxInit   func(context.Context) error
}

// RemoteMachine is the result of a short, non-interactive remote probe.
type RemoteMachine struct {
	Machine     string
	Home        string
	Shell       string
	OS          string
	Arch        string
	HasNexo     bool
	NexoPath    string
	NexoVersion string
	HasTmux     bool
	HasHerdr    bool
	LocaleUTF8  bool
	Locale      string
	Reachable   bool
	Backend     string
	Error       error
}

// ParseSSHHosts returns concrete Host aliases from ssh_config. Wildcard-only
// Host declarations and negated patterns are intentionally ignored.
func ParseSSHHosts(contents string) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, raw := range strings.Split(contents, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "host") {
			continue
		}
		for _, host := range fields[1:] {
			if strings.ContainsAny(host, "*?!") || host == "" || seen[strings.ToLower(host)] {
				continue
			}
			seen[strings.ToLower(host)] = true
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// DefaultPaths returns common project roots that exist, in stable order.
func DefaultPaths(home string, exists func(string) bool) []string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	candidates := []string{"src", "personal", "projects", "work", "code"}
	paths := make([]string, 0, len(candidates))
	for _, name := range candidates {
		path := filepath.Join(home, name)
		if exists == nil {
			_, err := os.Stat(path)
			if err != nil {
				continue
			}
		} else if !exists(path) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// TmuxBlock is the complete setup-owned tmux configuration.
func TmuxBlock(tpm bool) string {
	lines := []string{"set -g focus-events on"}
	if tpm {
		lines = append(lines, "set -g @plugin 'vieitesss/nexo'")
	} else {
		lines = append(lines, "if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell \"nexo tmux init --backend=tmux\"'")
	}
	return strings.Join(lines, "\n")
}

// HerdrBlock contains only opt-in example bindings. The adapter itself owns
// actions; setup merely offers the README defaults and never installs keys by
// default unless the user selected them in the wizard.
func HerdrBlock(tuiKey, agentsKey string) string {
	var b strings.Builder
	if strings.TrimSpace(tuiKey) != "" {
		fmt.Fprintf(&b, "[[keys.command]]\nkey = %s\ntype = \"plugin_action\"\ncommand = \"nexo.tui\"\ndescription = \"open nexo TUI\"\n\n", strconv.Quote(tuiKey))
	}
	if strings.TrimSpace(agentsKey) != "" {
		fmt.Fprintf(&b, "[[keys.command]]\nkey = %s\ntype = \"plugin_action\"\ncommand = \"nexo.tui-agents\"\ndescription = \"open nexo agents\"\n", strconv.Quote(agentsKey))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ProbeRemote checks reachability and all setup facts in one SSH call.
func ProbeRemote(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig string) RemoteMachine {
	result := RemoteMachine{Machine: strings.TrimSpace(machine)}
	if result.Machine == "" {
		result.Error = errors.New("machine alias is empty")
		return result
	}
	script := `set +e
printf 'home=%s\n' "$HOME"
printf 'shell=%s\n' "${SHELL:-}"
uname -sm 2>/dev/null | awk '{print "platform=" $0}'
printf 'nexo=%s\n' "$(command -v nexo 2>/dev/null)"
printf 'tmux=%s\n' "$(command -v tmux 2>/dev/null)"
printf 'herdr=%s\n' "$(command -v herdr 2>/dev/null)"
printf 'locale=%s\n' "$(locale charmap 2>/dev/null)"
`
	out, err := run(ctx, commandRunner, result.Machine, sshConfig, "sh", "-c", script)
	if err != nil {
		result.Error = fmt.Errorf("unreachable: %w", err)
		return result
	}
	result.Reachable = true
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "home":
			result.Home = value
		case "shell":
			result.Shell = value
		case "platform":
			fields := strings.Fields(value)
			if len(fields) >= 2 {
				result.OS = normalizeOS(fields[0])
				result.Arch = normalizeArch(fields[1])
			}
		case "nexo":
			result.NexoPath = value
			result.HasNexo = value != ""
		case "tmux":
			result.HasTmux = value != ""
		case "herdr":
			result.HasHerdr = value != ""
		case "locale":
			result.Locale = value
			result.LocaleUTF8 = strings.Contains(strings.ToUpper(value), "UTF-8") || strings.Contains(strings.ToUpper(value), "UTF8")
		}
	}
	if result.HasNexo {
		if version, versionErr := run(ctx, commandRunner, result.Machine, sshConfig, result.NexoPath, "--version"); versionErr == nil {
			result.NexoVersion = strings.TrimSpace(string(version))
		}
	}
	return result
}

func normalizeOS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "linux":
		return "linux"
	case "darwin", "macos":
		return "darwin"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}
func normalizeArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func chooseBackend(ctx context.Context, in Input, options Options) (string, error) {
	selected := strings.ToLower(strings.TrimSpace(options.Backend))
	if selected == "" {
		selected = strings.ToLower(strings.TrimSpace(in.SelectedBackend))
	}
	if selected == "tmux" || selected == "herdr" {
		return selected, nil
	}
	var tmuxOK, herdrOK bool
	if in.Runner != nil {
		if _, err := in.Runner.Run(ctx, "sh", "-c", "command -v tmux"); err == nil {
			tmuxOK = true
		}
		if _, err := in.Runner.Run(ctx, "sh", "-c", "command -v herdr"); err == nil {
			herdrOK = true
		}
	}
	switch {
	case tmuxOK && !herdrOK:
		return "tmux", nil
	case herdrOK && !tmuxOK:
		return "herdr", nil
	case tmuxOK && herdrOK && in.TTY:
		return "tmux", nil
	case tmuxOK && herdrOK:
		if options.Check {
			return "none", nil
		}
		return "", errors.New("both tmux and Herdr are available; pass --backend or run setup in a multiplexer")
	}
	if options.Check {
		return "none", nil
	}
	return "", errors.New("no tmux or Herdr installation found; install one or pass --backend")
}

func sshConfigPath(options Options, home string) string {
	if options.SSHConfig != "" {
		return options.SSHConfig
	}
	return filepath.Join(home, ".ssh", "config")
}

func configPlan(options Options, home string, paths []string, machines []string, backends map[string]string) (Change, error) {
	path := options.ConfigPath
	if path == "" {
		path = config.DefaultPathFrom(home, "")
	}
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, err
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("nexo-setup-%d.toml", time.Now().UnixNano()))
	defer func() { _ = os.Remove(tmp) }()
	if len(before) > 0 {
		if err := os.WriteFile(tmp, before, 0o600); err != nil {
			return Change{}, err
		}
	}
	for _, projectPath := range paths {
		if _, _, err := config.AddProjectPath(tmp, projectPath, home); err != nil {
			return Change{}, err
		}
	}
	notify := options.Notify
	if notify == nil {
		value := true
		notify = &value
	}
	if _, err := config.MergeSetup(tmp, config.SetupValues{Notify: notify, Machines: machines, Backends: backends}); err != nil {
		return Change{}, err
	}
	after, err := os.ReadFile(tmp)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, err
	}
	return Change{Path: path, Before: before, After: after}, nil
}

// Run performs setup, printing all planned edits before applying them.
func Run(ctx context.Context, in Input, options Options) error {
	if in.Runner == nil {
		return errors.New("setup requires a command runner")
	}
	if in.Home == "" {
		in.Home, _ = os.UserHomeDir()
	}
	if options.ConfigPath == "" {
		options.ConfigPath = in.ConfigPath
	}
	if options.Notify == nil {
		path := options.ConfigPath
		if path == "" {
			path = config.DefaultPathFrom(in.Home, "")
		}
		if existing, loadErr := config.LoadOptional(path); loadErr != nil {
			return loadErr
		} else if existing.Agents.Notify != nil {
			value := *existing.Agents.Notify
			options.Notify = &value
		}
	}
	backend, err := chooseBackend(ctx, in, options)
	if err != nil {
		return err
	}
	if backend == "herdr" && !options.Check {
		if err := requireHerdrVersion(ctx, in.Runner, "", ""); err != nil {
			return err
		}
	}
	if options.Check {
		return runCheck(ctx, in, options, backend)
	}
	machines, err := selectedMachines(options, in, sshConfigPath(options, in.Home))
	if err != nil {
		return err
	}
	var tuiKey, agentsKey string
	if in.TTY && !options.Yes {
		hosts, _ := os.ReadFile(sshConfigPath(options, in.Home))
		answers, promptErr := tui.PromptSetup(ctx, in.Stdin, in.Stdout, tui.SetupPromptDefaults{Backend: backend, Notify: options.Notify == nil || *options.Notify, Hosts: ParseSSHHosts(string(hosts))})
		if promptErr != nil {
			return promptErr
		}
		backend = answers.Backend
		if backend != "tmux" && backend != "herdr" {
			return fmt.Errorf("invalid setup backend %q", backend)
		}
		options.Machines = answers.Hosts
		machines = answers.Hosts
		notify := answers.Notify
		options.Notify = &notify
		tuiKey, agentsKey = answers.TUIKey, answers.AgentsKey
	}
	paths := options.Paths
	if len(paths) == 0 {
		paths = DefaultPaths(in.Home, nil)
	}
	var changes []Change
	if backend == "tmux" {
		tpm := false
		if _, statErr := os.Stat(filepath.Join(in.Home, ".tmux", "plugins", "tpm")); statErr == nil {
			tpm = true
		}
		tmuxChange, err := ReadChange("local", filepath.Join(in.Home, ".tmux.conf"), TmuxBlock(tpm))
		if err != nil {
			return err
		}
		if tmuxChange.Changed() {
			changes = append(changes, tmuxChange)
		} else {
			writeLine(in.Stdout, "%s: ok\n", tmuxChange.Path)
		}
	} else {
		herdrPath := filepath.Join(in.Home, ".config", "herdr", "config.toml")
		herdrChange, err := ReadChange("local", herdrPath, HerdrBlock(tuiKey, agentsKey))
		if err != nil {
			return err
		}
		if herdrChange.Changed() {
			changes = append(changes, herdrChange)
		} else {
			writeLine(in.Stdout, "%s: ok\n", herdrChange.Path)
		}
	}
	remotePlans := make([]RemoteMachine, len(machines))
	var wg sync.WaitGroup
	wg.Add(len(machines))
	sshConfig := sshConfigPath(options, in.Home)
	for i, machine := range machines {
		go func(index int, alias string) {
			defer wg.Done()
			remotePlans[index] = ProbeRemote(ctx, in.Runner, alias, sshConfig)
		}(i, machine)
	}
	wg.Wait()
	remoteBackends := make(map[string]string, len(remotePlans))
	for i := range remotePlans {
		remote := &remotePlans[i]
		if remote.Error != nil {
			writeLine(in.Stdout, "%s: failed (%v)\n", remote.Machine, remote.Error)
			continue
		}
		if remote.HasTmux && remote.HasHerdr {
			remote.Backend = backend
		} else if remote.HasTmux {
			remote.Backend = "tmux"
		} else if remote.HasHerdr {
			remote.Backend = "herdr"
		}
		if remote.Backend != "" {
			remoteBackends[remote.Machine] = remote.Backend
		}
		writeLine(in.Stdout, "%s: reachable (%s/%s), backend=%s\n", remote.Machine, remote.OS, remote.Arch, remote.Backend)
	}
	cfgChange, err := configPlan(options, in.Home, paths, machines, remoteBackends)
	if err != nil {
		return fmt.Errorf("plan config: %w", err)
	}
	if cfgChange.Changed() {
		changes = append([]Change{cfgChange}, changes...)
	} else {
		writeLine(in.Stdout, "%s: ok\n", cfgChange.Path)
	}
	for _, remote := range remotePlans {
		if remote.Error == nil {
			if remote.HasNexo && remote.LocaleUTF8 && remote.Backend != "" {
				writeLine(in.Stdout, "%s: ok (binary=%s locale=%s backend=%s)\n", remote.Machine, remote.NexoPath, remote.Locale, remote.Backend)
			} else {
				writeLine(in.Stdout, "--- %s (remote %s)\n  binary=%s path=%s locale=%s backend=%s\n", remote.Machine, remote.Machine, boolString(remote.HasNexo), remote.NexoPath, remote.Locale, remote.Backend)
			}
		}
	}
	for _, change := range changes {
		printChange(in.Stdout, change)
	}
	if !options.Yes && !in.TTY && !options.DryRun {
		return errors.New("setup needs confirmation; use --yes in non-interactive mode")
	}
	if !options.Yes && in.TTY {
		writeLine(in.Stdout, "Apply setup changes? [y/N] ")
		var answer string
		_, _ = fmt.Fscan(os.Stdin, &answer)
		if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
			return errors.New("setup canceled")
		}
	}
	if options.DryRun {
		writeLine(in.Stdout, "dry run: no changes written\n")
		return nil
	}
	for _, change := range changes {
		if err := ApplyChange(change); err != nil {
			return err
		}
		writeLine(in.Stdout, "%s: updated %s\n", change.Machine, change.Path)
	}
	if backend == "tmux" {
		if in.LocalTmuxInit != nil {
			if err := in.LocalTmuxInit(ctx); err != nil {
				return fmt.Errorf("initialize local tmux: %w", err)
			}
		}
		if _, err := in.Runner.Run(ctx, "tmux", "source-file", filepath.Join(in.Home, ".tmux.conf")); err != nil {
			return fmt.Errorf("reload tmux: %w", err)
		}
	} else {
		if _, err := in.Runner.Run(ctx, "herdr", "plugin", "install", "vieitesss/nexo"); err != nil {
			return fmt.Errorf("install Herdr plugin: %w", err)
		}
		if _, err := in.Runner.Run(ctx, "herdr", "server", "reload-config"); err != nil {
			return fmt.Errorf("reload Herdr config: %w", err)
		}
	}
	var failures []string
	var applyWG sync.WaitGroup
	var mu sync.Mutex
	for _, remote := range remotePlans {
		if remote.Error != nil {
			failures = append(failures, remote.Machine)
			continue
		}
		applyWG.Add(1)
		go func(machine RemoteMachine) {
			defer applyWG.Done()
			if err := applyRemote(ctx, in, options, machine.Backend, sshConfig, machine, paths); err != nil {
				mu.Lock()
				failures = append(failures, machine.Machine+": "+err.Error())
				mu.Unlock()
			}
		}(remote)
	}
	applyWG.Wait()
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("setup failed on %s", strings.Join(failures, ", "))
	}
	return nil
}

func selectedMachines(options Options, in Input, sshConfig string) ([]string, error) {
	if options.LocalOnly {
		return nil, nil
	}
	if len(options.Machines) > 0 {
		return unique(options.Machines), nil
	}
	contents, err := os.ReadFile(sshConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	hosts := ParseSSHHosts(string(contents))
	if len(hosts) == 0 {
		return nil, nil
	}
	if options.Yes {
		return hosts, nil
	}
	if !in.TTY {
		return nil, errors.New("remote machines are not selected; use --local-only, --machine ALIAS, or --yes")
	}
	// The Bubble Tea prompt defaults to all discovered aliases. The command's
	// explicit --machine remains the scriptable way to narrow the selection.
	return hosts, nil
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

// FormatDiff renders a compact, reviewable diff for one planned file.
func FormatDiff(change Change) string {
	if !change.Changed() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (%s)\n+++ %s\n", change.Path, change.Machine, change.Path)
	for _, line := range strings.Split(strings.TrimSuffix(string(change.Before), "\n"), "\n") {
		if line != "" {
			b.WriteString("-" + line + "\n")
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(change.After), "\n"), "\n") {
		if line != "" {
			b.WriteString("+" + line + "\n")
		}
	}
	return b.String()
}

func printChange(w io.Writer, change Change) {
	if diff := FormatDiff(change); diff != "" {
		writeLine(w, "%s", diff)
	}
}

func applyRemote(ctx context.Context, in Input, options Options, backend, sshConfig string, remote RemoteMachine, paths []string) error {
	if backend == "" {
		return errors.New("no tmux or Herdr multiplexer detected")
	}
	home := remote.Home
	if home == "" {
		home = "$HOME"
	}
	if !remote.HasNexo {
		source, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find local nexo binary: %w", err)
		}
		destination := filepath.Join(home, ".local", "bin", "nexo")
		platform := install.LocalPlatform()
		if remote.OS != platform.OS || remote.Arch != platform.Arch {
			return fmt.Errorf("remote platform %s/%s differs from local %s/%s; cross-compilation is unavailable", remote.OS, remote.Arch, platform.OS, platform.Arch)
		}
		if err := install.CopyAtomic(ctx, install.NewRemoteTarget(in.Runner, remote.Machine, sshConfig), source, destination); err != nil {
			return err
		}
	}
	shellFile := remoteShellFile(remote.Shell, home)
	pathChange, err := remoteManagedChange(ctx, in.Runner, remote.Machine, sshConfig, shellFile, "export PATH=\"$HOME/.local/bin:$PATH\"")
	if err != nil {
		return err
	}
	if pathChange.Changed() {
		if err := applyRemoteChange(ctx, in.Runner, remote.Machine, sshConfig, shellFile, pathChange.After); err != nil {
			return err
		}
	}
	if out, err := run(ctx, in.Runner, remote.Machine, sshConfig, "sh", "-c", "command -v nexo"); err != nil || strings.TrimSpace(string(out)) == "" {
		return errors.New("nexo is not visible in non-interactive SSH shell")
	}
	if !remote.LocaleUTF8 {
		return fmt.Errorf("locale is not UTF-8 (%s); set a UTF-8 locale and rerun", remote.Locale)
	}
	if backend == "tmux" && remote.HasTmux {
		path := filepath.Join(home, ".tmux.conf")
		change, err := remoteManagedChange(ctx, in.Runner, remote.Machine, sshConfig, path, TmuxBlock(false))
		if err != nil {
			return err
		}
		if change.Changed() {
			if err := applyRemoteChange(ctx, in.Runner, remote.Machine, sshConfig, path, change.After); err != nil {
				return err
			}
		}
		if _, err := run(ctx, in.Runner, remote.Machine, sshConfig, "tmux", "source-file", path); err != nil {
			return err
		}
		if _, err := run(ctx, in.Runner, remote.Machine, sshConfig, "nexo", "tmux", "init", "--backend=tmux"); err != nil {
			return err
		}
	} else if remote.HasHerdr {
		if err := requireHerdrVersion(ctx, in.Runner, remote.Machine, sshConfig); err != nil {
			return err
		}
		if _, err := run(ctx, in.Runner, remote.Machine, sshConfig, "herdr", "plugin", "install", "vieitesss/nexo"); err != nil {
			return err
		}
		if _, err := run(ctx, in.Runner, remote.Machine, sshConfig, "herdr", "server", "reload-config"); err != nil {
			return err
		}
	}
	if options.RemoteConfig {
		cfgPath := filepath.Join(home, ".config", "nexo", "config.toml")
		localPath := options.ConfigPath
		if localPath == "" {
			localPath = config.DefaultPathFrom(in.Home, "")
		}
		local, readErr := os.ReadFile(localPath)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		agents := onlyAgentsTable(local)
		if len(agents) > 0 {
			if err := applyRemoteChange(ctx, in.Runner, remote.Machine, sshConfig, cfgPath, agents); err != nil {
				return err
			}
		}
	}
	_ = paths
	return nil
}

func onlyAgentsTable(contents []byte) []byte {
	lines := strings.SplitAfter(string(contents), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		trim := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if trim == "[agents]" {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			end = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	return []byte(strings.Join(lines[start:end], ""))
}

func remoteShellFile(shell, home string) string {
	shell = strings.ToLower(shell)
	switch {
	case strings.Contains(shell, "zsh"):
		return filepath.Join(home, ".zshenv")
	case strings.Contains(shell, "fish"):
		return filepath.Join(home, ".config", "fish", "config.fish")
	case strings.Contains(shell, "bash"):
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

func remoteManagedChange(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, path, body string) (Change, error) {
	out, err := run(ctx, commandRunner, machine, sshConfig, "sh", "-c", "cat "+ShellQuote(path)+" 2>/dev/null || true")
	if err != nil {
		return Change{}, err
	}
	after, changed, err := ReplaceManagedBlock(string(out), body)
	if err != nil {
		return Change{}, err
	}
	if !changed {
		after = string(out)
	}
	return Change{Machine: machine, Path: path, Before: out, After: []byte(after)}, nil
}

func applyRemoteChange(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, path string, contents []byte) error {
	script := "set -eu; path=" + ShellQuote(path) + "; dir=$(dirname \"$path\"); mkdir -p \"$dir\"; if test -f \"$path\" && test ! -e \"$path.nexo.bak\"; then cp -p \"$path\" \"$path.nexo.bak\"; fi; tmp=$(mktemp \"$dir/.nexo-setup.XXXXXX\"); trap 'rm -f \"$tmp\"' EXIT; cat >\"$tmp\"; if test -f \"$path\"; then chmod " + "$(stat -c %a \"$path\" 2>/dev/null || stat -f %Lp \"$path\")" + " \"$tmp\" 2>/dev/null || true; fi; mv -f \"$tmp\" \"$path\""
	_, err := runInput(ctx, commandRunner, machine, sshConfig, contents, "sh", "-c", script)
	return err
}

func runCheck(ctx context.Context, in Input, options Options, backend string) error {
	localPathBytes, localPathErr := in.Runner.Run(ctx, "sh", "-c", "command -v nexo")
	localPath := strings.TrimSpace(string(localPathBytes))
	localVersion := ""
	if localPathErr == nil && localPath != "" {
		if out, err := in.Runner.Run(ctx, localPath, "--version"); err == nil {
			localVersion = strings.TrimSpace(string(out))
		}
	}
	writeLine(in.Stdout, "local: backend=%s config=%s binary=%t path=%s version=%s focus=%s init/plugin=%s watcher=%s\n", backend, options.ConfigPath, localPath != "", localPath, localVersion, boolString(backend == "tmux"), boolString(backend == "tmux" || backend == "herdr"), watcherStatus(backend == "tmux"))
	var machines []string
	if len(options.Machines) > 0 || options.LocalOnly {
		machines = unique(options.Machines)
	} else {
		path := options.ConfigPath
		if path == "" {
			path = config.DefaultPathFrom(in.Home, "")
		}
		if existing, loadErr := config.LoadOptional(path); loadErr != nil {
			return loadErr
		} else {
			machines = existing.Remote.ConfiguredMachines()
		}
	}
	sshConfig := sshConfigPath(options, in.Home)
	results := make([]RemoteMachine, len(machines))
	var wg sync.WaitGroup
	wg.Add(len(machines))
	for i, machine := range machines {
		go func(i int, machine string) {
			defer wg.Done()
			results[i] = ProbeRemote(ctx, in.Runner, machine, sshConfig)
		}(i, machine)
	}
	wg.Wait()
	var failed []string
	for i := range results {
		if results[i].HasTmux && results[i].HasHerdr {
			results[i].Backend = backend
		} else if results[i].HasTmux {
			results[i].Backend = "tmux"
		} else if results[i].HasHerdr {
			results[i].Backend = "herdr"
		}
	}
	for _, result := range results {
		if result.Error != nil {
			writeLine(in.Stdout, "%s: unreachable (%v)\n", result.Machine, result.Error)
			failed = append(failed, result.Machine)
			continue
		}
		writeLine(in.Stdout, "%s: binary=%t path=%s version=%s locale=%s focus=%t init/plugin=%s watcher=%s backend=%s\n", result.Machine, result.HasNexo, result.NexoPath, result.NexoVersion, result.Locale, result.HasTmux, boolString(result.HasTmux || result.HasHerdr), watcherStatus(result.HasTmux), result.Backend)
		if !result.HasNexo || !result.LocaleUTF8 {
			failed = append(failed, result.Machine)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("check failed on %s", strings.Join(failed, ", "))
	}
	return nil
}
func requireHerdrVersion(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig string) error {
	out, err := run(ctx, commandRunner, machine, sshConfig, "herdr", "--version")
	if err != nil {
		return fmt.Errorf("check Herdr version: %w", err)
	}
	var major, minor, patch int
	for _, field := range strings.Fields(string(out)) {
		field = strings.TrimPrefix(field, "v")
		if _, scanErr := fmt.Sscanf(field, "%d.%d.%d", &major, &minor, &patch); scanErr == nil {
			break
		}
	}
	if major == 0 && minor == 0 && patch == 0 {
		return errors.New("unable to determine Herdr version (need 0.9.0 or newer)")
	}
	if major == 0 && minor < 9 {
		return fmt.Errorf("herdr %d.%d.%d is too old (need 0.9.0 or newer)", major, minor, patch)
	}
	return nil
}

func boolString(v bool) string {
	if v {
		return "ok"
	}
	return "missing"
}
func watcherStatus(tmux bool) string {
	if tmux {
		return "check via nexo tmux init"
	}
	return "native"
}

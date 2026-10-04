package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vieitesss/nexo/internal/config"
	"github.com/vieitesss/nexo/internal/install"
	"github.com/vieitesss/nexo/internal/runner"
	"github.com/vieitesss/nexo/internal/tui"
	"github.com/vieitesss/nexo/internal/upgrade"
	"github.com/vieitesss/nexo/internal/version"
)

// Options are the non-interactive setup controls.
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
	FixLocale    bool
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
	// SelectedSource says where SelectedBackend came from, as backend.Selection
	// reports it: "flag", "config" or "auto-detect".
	SelectedSource string
	ConfigPath     string
	LocalTmuxInit  func(context.Context) error
}

// RemoteMachine is the result of a short, non-interactive remote probe.
type RemoteMachine struct {
	Machine        string
	Home           string
	Shell          string
	OS             string
	Arch           string
	HasNexo        bool
	NexoPath       string
	NexoVersion    string
	HasTmux        bool
	HasHerdr       bool
	HasHerdrPlugin bool
	LocaleUTF8     bool
	Locale         string
	UTF8Locale     string
	WatcherRunning bool
	TmuxRunning    bool
	HerdrRunning   bool
	TmuxFocusOK    bool
	TmuxHooksOK    bool
	Reachable      bool
	Backend        string
	Error          error
}

// ParseSSHHosts returns concrete Host aliases from ssh_config. Inline comments
// are ignored, as are wildcard and negated declarations.
func ParseSSHHosts(contents string) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, raw := range strings.Split(contents, "\n") {
		line := raw
		if index := strings.IndexByte(line, '#'); index >= 0 {
			line = line[:index]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "host") {
			continue
		}
		for _, host := range fields[1:] {
			if strings.ContainsAny(host, "*?!") || seen[strings.ToLower(host)] {
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
			if _, err := os.Stat(path); err != nil {
				continue
			}
		} else if !exists(path) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// TmuxBlock is the complete setup-owned tmux configuration. Optional keys are
// accepted for the guided binding choice while preserving the old API.
func TmuxBlock(tpm bool, keys ...string) string {
	lines := []string{"set -g focus-events on"}
	if tpm {
		lines = append(lines, "set -g @plugin 'vieitesss/nexo'")
	} else {
		lines = append(lines, "if-shell 'command -v nexo >/dev/null 2>&1' 'run-shell \"nexo tmux init --backend=tmux\"'")
	}
	if len(keys) > 0 && strings.TrimSpace(keys[0]) != "" {
		lines = append(lines, tmuxBind(keys[0], "nexo tui"))
	}
	if len(keys) > 1 && strings.TrimSpace(keys[1]) != "" {
		lines = append(lines, tmuxBind(keys[1], "nexo tui agents"))
	}
	return strings.Join(lines, "\n")
}

// tmuxBind binds prefix+KEY in the prefix table and any other key in the root
// table, so either nexo action can be reached with or without the prefix.
func tmuxBind(key, command string) string {
	tmuxKey, afterPrefix := config.SplitTmuxBinding(strings.TrimSpace(key))
	table := "-n "
	if afterPrefix {
		table = ""
	}
	return "bind " + table + tmuxKey + " display-popup -E -B -w 100% -h 100% \"" + command + "\""
}

// parseTmuxBind reads a bind/bind-key line from a config file or from
// tmux list-keys. It returns the key in setup's spelling (prefix+a for the
// prefix table) and false for lines that bind outside root and prefix.
func parseTmuxBind(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || (fields[0] != "bind" && fields[0] != "bind-key") {
		return "", false
	}
	table := "prefix"
	index := 1
	for index < len(fields) && strings.HasPrefix(fields[index], "-") {
		flag := fields[index]
		index++
		switch flag {
		case "-n":
			table = "root"
		case "-T":
			if index < len(fields) {
				table = fields[index]
				index++
			}
		case "-N":
			// The note is quoted and may contain spaces; nexo never writes one,
			// so a line carrying it is left alone.
			return "", false
		}
	}
	if index >= len(fields) {
		return "", false
	}
	switch table {
	case "root":
		return fields[index], true
	case "prefix":
		return config.TmuxPrefixMarker + fields[index], true
	}
	return "", false
}

// sameTmuxKey compares two keys in setup's spelling the way tmux resolves
// them: ^x is C-x, modifiers are case-insensitive, and so are key names.
func sameTmuxKey(a, b string) bool {
	return canonicalTmuxKey(a) == canonicalTmuxKey(b)
}

func canonicalTmuxKey(key string) string {
	key, afterPrefix := config.SplitTmuxBinding(key)
	var mods strings.Builder
	if strings.HasPrefix(key, "^") && len(key) > 1 {
		mods.WriteString("C-")
		key = key[1:]
	}
	for len(key) > 2 && key[1] == '-' {
		mods.WriteString(strings.ToUpper(key[:2]))
		key = key[2:]
	}
	if len([]rune(key)) > 1 {
		key = strings.ToLower(key)
	}
	if afterPrefix {
		return config.TmuxPrefixMarker + mods.String() + key
	}
	return mods.String() + key
}

// HerdrBlock contains only user-selected bindings. Empty choices keep the
// plugin opt-in and produce an empty managed block.
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

const tmuxReadinessScript = `printf 'watcher=%s\n' "$(pgrep -f '[n]exo.*agents.*watch' >/dev/null 2>&1 && echo yes || echo no)"
printf 'tmux_running=%s\n' "$(tmux list-sessions >/dev/null 2>&1 && echo yes || echo no)"
printf 'tmux_focus=%s\n' "$(tmux show-options -gv focus-events 2>/dev/null)"
printf 'tmux_hooks=%s\n' "$(pane=$(tmux show-hooks -g pane-focus-in 2>/dev/null); client=$(tmux show-hooks -g client-session-changed 2>/dev/null); printf '%s' \"$pane\" | grep -q 'pane-focus-in.*NEXO_AGENT_FOCUS_HOOK=1' && printf '%s' \"$client\" | grep -q 'client-session-changed.*NEXO_AGENT_FOCUS_HOOK=1' && echo yes || echo no)"
`

// ProbeRemote checks reachability and setup facts in one bounded SSH call.
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
printf 'plugin=%s\n' "$(herdr plugin list 2>/dev/null | grep -q 'vieitesss/nexo' && echo yes || echo no)"
printf 'locale=%s\n' "$(locale charmap 2>/dev/null)"
printf 'utf8locale=%s\n' "$(locale -a 2>/dev/null | grep -iE '(^|[._-])(utf-?8|c\.utf8)$' | head -n 1)"
` + tmuxReadinessScript + `printf 'herdr_running=%s\n' "$(herdr status server --json 2>/dev/null | grep -q '"running"[[:space:]]*:[[:space:]]*true' && echo yes || echo no)"
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
		case "plugin":
			result.HasHerdrPlugin = value == "yes"
		case "locale":
			result.Locale = value
			upper := strings.ToUpper(value)
			result.LocaleUTF8 = strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
		case "utf8locale":
			result.UTF8Locale = value
		case "watcher":
			result.WatcherRunning = value == "yes"
		case "tmux_running":
			result.TmuxRunning = value == "yes"
		case "herdr_running":
			result.HerdrRunning = value == "yes"
		case "tmux_focus":
			result.TmuxFocusOK = strings.EqualFold(value, "on")
		case "tmux_hooks":
			result.TmuxHooksOK = value == "yes"
		}
	}
	readiness := parseTmuxReadiness(out)
	result.WatcherRunning = readiness.WatcherRunning
	result.TmuxRunning = readiness.TmuxRunning
	result.TmuxFocusOK = readiness.TmuxFocusOK
	result.TmuxHooksOK = readiness.TmuxHooksOK
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

// chooseBackend picks the backend setup integrates with and says how it knew.
// An empty reason means the answer is only a suggestion: both multiplexers are
// installed and nothing chose between them, so the form asks.
func chooseBackend(ctx context.Context, in Input, options Options) (string, string, error) {
	selected := strings.ToLower(strings.TrimSpace(options.Backend))
	reason := "set by --backend"
	if selected == "" || selected == "none" || selected == "auto" {
		selected = strings.ToLower(strings.TrimSpace(in.SelectedBackend))
		switch in.SelectedSource {
		case "flag":
			reason = "set by --backend"
		case "config":
			reason = "set in config"
		default:
			reason = "detected in this terminal"
		}
	}
	if selected == "tmux" || selected == "herdr" {
		return selected, reason, nil
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
		return "tmux", "the only one installed", nil
	case herdrOK && !tmuxOK:
		return "herdr", "the only one installed", nil
	case tmuxOK && herdrOK && in.TTY:
		return "tmux", "", nil
	case tmuxOK && herdrOK:
		return "", "", errors.New("both tmux and Herdr are available; pass --backend or run setup in a multiplexer")
	case options.Check:
		return "none", "", nil
	default:
		return "", "", errors.New("no tmux or Herdr installation found; install one or pass --backend")
	}
}

func sshConfigPath(options Options, home string) string {
	if options.SSHConfig != "" {
		return options.SSHConfig
	}
	return filepath.Join(home, ".ssh", "config")
}

func configPlan(options Options, home string, backendName string, paths []string, machines []string, backends map[string]string, notify *bool) (Change, error) {
	path := options.ConfigPath
	if path == "" {
		path = config.DefaultPathFrom(home, "")
	}
	before, mode, existed, err := readLocalFile(path)
	if err != nil {
		return Change{}, err
	}
	after, err := config.PrepareSetup(before, path, home, config.SetupValues{Notify: notify, Machines: machines, Backends: backends, Paths: paths})
	if err != nil {
		return Change{}, err
	}
	return Change{Path: path, Machine: "local", Before: before, After: after, Mode: mode, Existed: existed}, nil
}

func readLocalFile(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, 0, true, fmt.Errorf("read %s: %w", path, readErr)
		}
		return contents, info.Mode().Perm(), true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, fmt.Errorf("stat %s: %w", path, err)
	}
	return nil, 0o600, false, nil
}

type remotePlan struct {
	Machine      RemoteMachine
	Changes      []Change
	Backend      string
	Install      bool
	BinaryAt     string
	BuildRef     string
	BinarySource string
	Actions      []string
	Error        error
}

func planRemote(ctx context.Context, in Input, options Options, machine, sshConfig, configuredBackend string, localConfig []byte) remotePlan {
	return planRemoteWithFacts(ctx, in, options, machine, sshConfig, configuredBackend, localConfig, nil)
}

func planRemoteWithFacts(ctx context.Context, in Input, options Options, machine, sshConfig, configuredBackend string, localConfig []byte, knownFacts *RemoteMachine) remotePlan {
	machineCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	plan := remotePlan{}
	var facts RemoteMachine
	if knownFacts == nil {
		facts = ProbeRemote(machineCtx, in.Runner, machine, sshConfig)
	} else {
		facts = *knownFacts
	}
	plan.Machine = facts
	if facts.Error != nil {
		plan.Error = facts.Error
		return plan
	}
	if strings.TrimSpace(facts.Home) == "" {
		plan.Error = errors.New("remote probe did not return HOME; refusing to fabricate dotfile paths")
		return plan
	}
	kind := strings.ToLower(strings.TrimSpace(configuredBackend))
	if strings.TrimSpace(configuredBackend) == "" && facts.TmuxRunning && facts.HerdrRunning {
		plan.Error = errors.New("both tmux and Herdr are running; choose this machine's backend in [remote.backends] (or run setup in a TTY to choose it)")
	}
	if kind != "tmux" && kind != "herdr" && plan.Error == nil {
		switch {
		case facts.TmuxRunning && !facts.HerdrRunning:
			kind = "tmux"
		case facts.HerdrRunning && !facts.TmuxRunning:
			kind = "herdr"
		case facts.HasTmux && !facts.HasHerdr:
			kind = "tmux"
		case facts.HasHerdr && !facts.HasTmux:
			kind = "herdr"
		case facts.HasTmux && facts.HasHerdr:
			// Neither server is running, so tmux is the deterministic default;
			// ambiguity is reserved for two running servers.
			kind = "tmux"
		default:
			plan.Error = errors.New("remote has no supported multiplexer")
		}
	}
	plan.Backend = kind
	plan.Machine.Backend = kind
	if plan.Error != nil {
		return plan
	}
	if !facts.HasNexo {
		platform := install.LocalPlatform()
		if facts.OS != platform.OS || facts.Arch != platform.Arch {
			builder := &install.LocalBuilder{Runner: in.Runner}
			if !builder.Available(machineCtx) {
				plan.Error = fmt.Errorf("remote platform %s/%s differs from local %s/%s and no local Go toolchain is available for cross-build", facts.OS, facts.Arch, platform.OS, platform.Arch)
				return plan
			}
			ref, refErr := runningRevision()
			if refErr != nil {
				plan.Error = fmt.Errorf("remote platform %s/%s differs from local %s/%s: %w", facts.OS, facts.Arch, platform.OS, platform.Arch, refErr)
				return plan
			}
			plan.BuildRef = ref
			plan.Actions = append(plan.Actions, fmt.Sprintf("build nexo %s for %s/%s", ref, facts.OS, facts.Arch))
		}
		plan.Install = true
		plan.BinaryAt = filepath.Join(defaultRemoteHome(facts), ".local", "bin", "nexo")
		plan.Actions = append(plan.Actions, "install nexo at "+plan.BinaryAt)
	}
	shellPath := remoteShellFile(facts.Shell, defaultRemoteHome(facts))
	shellBody := shellPathBody(facts.Shell, facts.UTF8Locale, options.FixLocale && !facts.LocaleUTF8)
	pathChange, err := remoteShellChange(machineCtx, in.Runner, machine, sshConfig, shellPath, facts.Shell, shellBody)
	if err != nil {
		plan.Error = err
		return plan
	}
	if pathChange.Changed() {
		plan.Changes = append(plan.Changes, pathChange)
	}
	if !facts.LocaleUTF8 && !options.FixLocale {
		plan.Error = fmt.Errorf("locale is not UTF-8 (%s); rerun with --fix-locale after reviewing available locales", facts.Locale)
		return plan
	}
	if !facts.LocaleUTF8 && options.FixLocale {
		if strings.TrimSpace(facts.UTF8Locale) == "" {
			plan.Error = errors.New("remote has no available UTF-8 locale; refusing to modify dotfiles")
			return plan
		}
		plan.Actions = append(plan.Actions, "repair locale with "+facts.UTF8Locale)
	}
	if kind == "tmux" {
		path := filepath.Join(defaultRemoteHome(facts), ".tmux.conf")
		before, exists, readErr := readRemoteFile(machineCtx, in.Runner, machine, sshConfig, path)
		if readErr != nil {
			plan.Error = readErr
			return plan
		}
		tuiKey, agentsKey := bindingsFromContents(string(before), "tmux")
		if validationErr := validateBindings("tmux", tuiKey, agentsKey); validationErr != nil {
			plan.Error = validationErr
			return plan
		}
		after, _, replaceErr := ReplaceManagedBlock(string(before), TmuxBlock(false, tuiKey, agentsKey))
		if replaceErr != nil {
			plan.Error = fmt.Errorf("prepare %s: %w", path, replaceErr)
			return plan
		}
		change := Change{Machine: machine, Path: path, Before: before, After: []byte(after), Mode: 0o600, Existed: exists, Remote: true}
		if change.Changed() {
			plan.Changes = append(plan.Changes, change)
		}
		if len(plan.Changes) > 0 || !facts.HasNexo || !facts.WatcherRunning {
			plan.Actions = append(plan.Actions, "reload tmux and initialize watcher")
		}
		if !facts.WatcherRunning {
			plan.Actions = append(plan.Actions, "start tmux watcher")
		}
	} else {
		if err := requireHerdrVersion(machineCtx, in.Runner, machine, sshConfig); err != nil {
			plan.Error = err
			return plan
		}
		path := filepath.Join(defaultRemoteHome(facts), ".config", "herdr", "config.toml")
		before, exists, readErr := readRemoteFile(machineCtx, in.Runner, machine, sshConfig, path)
		if readErr != nil {
			plan.Error = readErr
			return plan
		}
		tuiKey, agentsKey := bindingsFromContents(string(before), "herdr")
		after, _, replaceErr := ReplaceManagedBlock(string(before), HerdrBlock(tuiKey, agentsKey))
		if replaceErr != nil {
			plan.Error = fmt.Errorf("prepare %s: %w", path, replaceErr)
			return plan
		}
		change := Change{Machine: machine, Path: path, Before: before, After: []byte(after), Mode: 0o600, Existed: exists, Remote: true}
		if change.Changed() {
			plan.Changes = append(plan.Changes, change)
		}
		if !facts.HasHerdrPlugin {
			plan.Actions = append(plan.Actions, "install Herdr plugin")
		}
		if change.Changed() {
			plan.Actions = append(plan.Actions, "reload Herdr config")
		}
	}
	if options.RemoteConfig && len(localConfig) > 0 {
		path := filepath.Join(defaultRemoteHome(facts), ".config", "nexo", "config.toml")
		before, exists, readErr := readRemoteFile(machineCtx, in.Runner, machine, sshConfig, path)
		if readErr != nil {
			plan.Error = readErr
			return plan
		}
		after, mergeErr := config.MergeAgents(before, localConfig, path)
		if mergeErr != nil {
			plan.Error = mergeErr
			return plan
		}
		change := Change{Machine: machine, Path: path, Before: before, After: after, Mode: 0o600, Existed: exists, Remote: true}
		if change.Changed() {
			plan.Changes = append(plan.Changes, change)
		}
	}
	return plan
}

var (
	releaseRevisionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
	commitRevisionPattern  = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
	moduleRevisionPattern  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{14}-[0-9a-fA-F]{7,}$`)
)

func runningRevision() (string, error) {
	raw := strings.TrimSpace(version.String())
	switch {
	case releaseRevisionPattern.MatchString(raw):
		return "v" + strings.TrimPrefix(raw, "v"), nil
	case commitRevisionPattern.MatchString(raw), moduleRevisionPattern.MatchString(raw):
		return raw, nil
	default:
		return "", fmt.Errorf("running binary has no usable release tag or commit (version %q); install a versioned build before configuring a different platform", raw)
	}
}

func defaultRemoteHome(machine RemoteMachine) string {
	return strings.TrimSpace(machine.Home)
}

func remoteShellChange(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig, path, shell, body string) (Change, error) {
	before, exists, err := readRemoteFile(ctx, commandRunner, machine, sshConfig, path)
	if err != nil {
		return Change{}, err
	}
	// Keep a successful prior locale repair when the current effective locale is
	// already UTF-8. Removing LC_ALL from the managed block makes the next
	// invocation undo the repair it just performed.
	body = preserveManagedLocale(string(before), body)
	after, changed, err := ReplaceManagedBlock(string(before), body)
	if err != nil {
		return Change{}, fmt.Errorf("prepare %s: %w", path, err)
	}
	// bash exits early for non-interactive shells in many user .bashrc files.
	// A new PATH block must precede that guard; an existing block stays in place.
	if changed && strings.Contains(strings.ToLower(shell), "bash") && !strings.Contains(string(before), ManagedStart) {
		insert := -1
		for _, marker := range []string{"case $- in", "[[ $- != *i* ]]", "[ -z \"$PS1\" ]"} {
			if index := strings.Index(after, marker); index >= 0 && (insert < 0 || index < insert) {
				insert = index
			}
		}
		if insert >= 0 {
			block := ManagedStart + "\n" + body + "\n" + ManagedEnd + "\n"
			after = string(before[:insert]) + block + string(before[insert:])
		}
	}
	return Change{Machine: machine, Path: path, Before: before, After: []byte(after), Mode: 0o600, Existed: exists, Remote: true}, nil
}

func preserveManagedLocale(before, body string) string {
	if strings.Contains(body, "LC_ALL") {
		return body
	}
	managed := managedBody(before)
	for _, line := range strings.Split(managed, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "LC_ALL") && (strings.HasPrefix(trimmed, "export LC_ALL=") || strings.HasPrefix(trimmed, "set -gx LC_ALL ")) {
			if strings.TrimSpace(body) == "" {
				return trimmed
			}
			return body + "\n" + trimmed
		}
	}
	return body
}

func shellPathBody(shell, locale string, repairLocale bool) string {
	shell = strings.ToLower(shell)
	lines := []string{}
	switch {
	case strings.Contains(shell, "fish"):
		lines = append(lines, "fish_add_path --prepend $HOME/.local/bin")
	default:
		lines = append(lines, "export PATH=\"$HOME/.local/bin:$PATH\"")
	}
	if repairLocale && strings.TrimSpace(locale) != "" {
		if strings.Contains(shell, "fish") {
			lines = append(lines, "set -gx LC_ALL "+strconv.Quote(strings.TrimSpace(locale)))
		} else {
			lines = append(lines, "export LC_ALL="+ShellQuote(strings.TrimSpace(locale)))
		}
	}
	return strings.Join(lines, "\n")
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

// Run performs planning for every selected machine, prints the complete plan,
// then applies exactly that plan after one confirmation.
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
	if options.ConfigPath == "" {
		options.ConfigPath = config.DefaultPathFrom(in.Home, "")
	}
	if options.Notify == nil {
		if existing, err := config.LoadOptional(options.ConfigPath); err != nil {
			return err
		} else if existing.Agents.Notify != nil {
			value := *existing.Agents.Notify
			options.Notify = &value
		} else {
			value := true
			options.Notify = &value
		}
	}
	backendName, backendReason, err := chooseBackend(ctx, in, options)
	if err != nil {
		return err
	}
	if options.Check {
		return runCheck(ctx, in, options, backendName)
	}
	sshConfig := sshConfigPath(options, in.Home)
	machines, err := selectedMachines(options, in, sshConfig)
	if err != nil {
		return err
	}
	configuredBackends := map[string]string{}
	theme := config.ThemeAuto
	var configuredPaths []string
	if existing, loadErr := config.LoadOptional(options.ConfigPath); loadErr == nil {
		for machine, value := range existing.Remote.Backends {
			configuredBackends[machine] = value
		}
		theme = existing.TUI.EffectiveTheme()
		configuredPaths = existing.Paths
	}
	paths := append([]string(nil), options.Paths...)
	var tuiKey, agentsKey string
	if !in.TTY || options.Yes {
		tuiKey, agentsKey = existingBindings(in.Home, backendName)
	}
	if in.TTY && !options.Yes {
		hostsFile, _ := os.ReadFile(sshConfigPath(options, in.Home))
		hosts := ParseSSHHosts(string(hostsFile))
		if len(options.Machines) > 0 {
			hosts = append([]string(nil), machines...)
		}
		bindings := make(map[string]tui.SetupBindings, 2)
		for _, name := range []string{"tmux", "herdr"} {
			tuiKey, agentsKey := existingBindings(in.Home, name)
			bindings[name] = tui.SetupBindings{TUIKey: tuiKey, AgentsKey: agentsKey}
		}
		answers, promptErr := tui.PromptSetup(ctx, in.Stdin, in.Stdout, tui.SetupPromptDefaults{Backend: backendName, BackendDetected: backendReason, Notify: *options.Notify, Hosts: hosts, SelectedHosts: machines, LocalOnly: options.LocalOnly, ScopeLocked: len(options.Machines) > 0, Paths: pathsOrDefaults(paths, configuredPaths, in.Home), Bindings: bindings, Theme: theme})
		if promptErr != nil {
			return promptErr
		}
		backendName = answers.Backend
		options.Machines = answers.Hosts
		machines = answers.Hosts
		paths = answers.Paths
		notify := answers.Notify
		options.Notify = &notify
		tuiKey, agentsKey = answers.TUIKey, answers.AgentsKey
	}
	if err := validateBindings(backendName, tuiKey, agentsKey); err != nil {
		return err
	}
	if backendName != "tmux" && backendName != "herdr" {
		return fmt.Errorf("invalid setup backend %q", backendName)
	}
	if backendName == "herdr" {
		if err := requireHerdrVersion(ctx, in.Runner, "", ""); err != nil {
			return err
		}
	}
	// This is the same bounded facts probe used by planning. It runs only
	// after the form has finalized the machine list, so typed aliases are
	// included and each result can be retained for the final plan.
	facts := probeRemoteMachines(ctx, in, machines, sshConfig)
	ambiguous := ambiguousFromFacts(machines, facts, configuredBackends, backendName)
	if in.TTY && !options.Yes && len(ambiguous) > 0 {
		answers, promptErr := tui.PromptRemoteBackends(ctx, in.Stdin, in.Stdout, ambiguous)
		if promptErr != nil {
			return promptErr
		}
		for machine, backend := range answers {
			configuredBackends[machine] = backend
		}
	}
	remoteBackends := make(map[string]string, len(machines))
	localChange, err := configPlan(options, in.Home, backendName, pathsOrDefaults(paths, configuredPaths, in.Home), machines, remoteBackends, options.Notify)
	if err != nil {
		return fmt.Errorf("plan config: %w", err)
	}
	changes := []Change{}
	if backendName == "tmux" {
		tpm := false
		if _, statErr := os.Stat(filepath.Join(in.Home, ".tmux", "plugins", "tpm")); statErr == nil {
			tpm = true
		}
		tmuxChange, changeErr := ReadChange("local", filepath.Join(in.Home, ".tmux.conf"), TmuxBlock(tpm, tuiKey, agentsKey))
		if changeErr != nil {
			return changeErr
		}
		if tmuxChange.Changed() {
			changes = append(changes, tmuxChange)
		}
	} else {
		herdrPath := filepath.Join(in.Home, ".config", "herdr", "config.toml")
		herdrChange, changeErr := ReadChange("local", herdrPath, HerdrBlock(tuiKey, agentsKey))
		if changeErr != nil {
			return changeErr
		}
		if herdrChange.Changed() {
			changes = append(changes, herdrChange)
		}
	}
	plans := make([]remotePlan, len(machines))
	var wg sync.WaitGroup
	for index, machine := range machines {
		wg.Add(1)
		go func(index int, machine string) {
			defer wg.Done()
			configured := configuredBackend(configuredBackends, machine)
			var knownFacts *RemoteMachine
			if index < len(facts) {
				knownFacts = &facts[index]
			}
			plans[index] = planRemoteWithFacts(ctx, in, options, machine, sshConfig, configured, localChange.After, knownFacts)
		}(index, machine)
	}
	wg.Wait()
	planningFailures := []string{}
	for index := range plans {
		plan := &plans[index]
		if plan.Error != nil {
			name := plan.Machine.Machine
			if name == "" {
				name = machines[index]
			}
			planningFailures = append(planningFailures, name+": "+plan.Error.Error())
			writeLine(in.Stdout, "%s: failed (%v)\n", name, plan.Error)
			continue
		}
		if plan.Backend != "" {
			remoteBackends[plan.Machine.Machine] = plan.Backend
		}
		writeLine(in.Stdout, "%s: planned (%s/%s, backend=%s)\n", plan.Machine.Machine, plan.Machine.OS, plan.Machine.Arch, plan.Backend)
		for _, action := range plan.Actions {
			writeLine(in.Stdout, "%s: action %s\n", plan.Machine.Machine, action)
		}
	}
	// Rebuild the config plan now that detected backend choices are known.
	localChange, err = configPlan(options, in.Home, backendName, pathsOrDefaults(paths, configuredPaths, in.Home), machines, remoteBackends, options.Notify)
	if err != nil {
		return fmt.Errorf("plan config: %w", err)
	}
	if localChange.Changed() {
		changes = append([]Change{localChange}, changes...)
	}
	for _, change := range changes {
		printChange(in.Stdout, change)
	}
	for _, plan := range plans {
		if plan.Error == nil {
			for _, change := range plan.Changes {
				printChange(in.Stdout, change)
			}
		}
	}
	for _, conflict := range bindingConflicts(ctx, in, in.Home, backendName, tuiKey, agentsKey) {
		writeLine(in.Stdout, "warning: binding %s is already present; setup will not remove it\n", conflict)
	}
	localRepair := localRepairNeeded(ctx, in, backendName)
	if len(changes) > 0 || localRepair {
		if backendName == "tmux" {
			writeLine(in.Stdout, "local: action reload tmux and initialize watcher\n")
		} else {
			if localRepair {
				writeLine(in.Stdout, "local: action install Herdr plugin\n")
			}
			if len(changes) > 0 {
				writeLine(in.Stdout, "local: action reload Herdr config\n")
			}
		}
	}
	if !options.DryRun {
		if !options.Yes && !in.TTY {
			return errors.New("setup needs confirmation; use --yes in non-interactive mode")
		}
		if !options.Yes {
			if err := confirm(in.Stdin, in.Stdout); err != nil {
				return err
			}
		}
	}
	if options.DryRun {
		writeLine(in.Stdout, "dry run: no changes written\n")
		if len(planningFailures) > 0 {
			sort.Strings(planningFailures)
			return fmt.Errorf("setup planning failed on %s", strings.Join(planningFailures, ", "))
		}
		return nil
	}
	localChanged := len(changes) > 0 || localRepair
	for _, change := range changes {
		if err := ApplyChange(change); err != nil {
			return err
		}
		writeLine(in.Stdout, "%s: updated %s\n", change.Machine, change.Path)
	}
	if !localChanged {
		writeLine(in.Stdout, "local: ok (no changes)\n")
	}
	if localChanged && backendName == "tmux" {
		if in.LocalTmuxInit != nil {
			if err := in.LocalTmuxInit(ctx); err != nil {
				return fmt.Errorf("initialize local tmux: %w", err)
			}
		}
		if _, err := in.Runner.Run(ctx, "tmux", "source-file", filepath.Join(in.Home, ".tmux.conf")); err != nil {
			return fmt.Errorf("reload tmux: %w", err)
		}
	} else if localChanged {
		pluginOutput, pluginErr := in.Runner.Run(ctx, "herdr", "plugin", "list")
		if pluginErr != nil || !pluginInstalled(string(pluginOutput)) {
			if _, err := in.Runner.Run(ctx, "herdr", "plugin", "install", "vieitesss/nexo"); err != nil {
				return fmt.Errorf("install Herdr plugin: %w", err)
			}
		}
		if len(changes) > 0 {
			if _, err := in.Runner.Run(ctx, "herdr", "server", "reload-config"); err != nil {
				return fmt.Errorf("reload Herdr config: %w", err)
			}
		}
	}
	crossBuilds, buildErr := prepareCrossBuilds(ctx, in, plans)
	if buildErr != nil {
		for index := range plans {
			if plans[index].Error == nil && plans[index].Install && isCrossPlatform(plans[index].Machine) && plans[index].BinarySource == "" {
				plans[index].Error = buildErr
			}
		}
	}
	defer func() {
		for _, path := range crossBuilds {
			_ = os.Remove(path)
		}
	}()
	results := make([]string, len(plans))
	var applyWG sync.WaitGroup
	for index := range plans {
		if plans[index].Error != nil {
			results[index] = plans[index].Machine.Machine + ": " + plans[index].Error.Error()
			continue
		}
		applyWG.Add(1)
		go func(index int) {
			defer applyWG.Done()
			machinePlan := plans[index]
			machineCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			if err := applyRemotePlan(machineCtx, in, options, sshConfig, machinePlan); err != nil {
				results[index] = machinePlan.Machine.Machine + ": " + err.Error()
				return
			}
			results[index] = machinePlan.Machine.Machine + ": ok"
		}(index)
	}
	applyWG.Wait()
	failures := []string{}
	for _, result := range results {
		if strings.HasSuffix(result, ": ok") {
			writeLine(in.Stdout, "%s\n", result)
		} else if result != "" {
			failures = append(failures, result)
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("setup failed on %s", strings.Join(failures, ", "))
	}
	return nil
}

func isCrossPlatform(machine RemoteMachine) bool {
	platform := install.LocalPlatform()
	return machine.OS != platform.OS || machine.Arch != platform.Arch
}

// prepareCrossBuilds runs the potentially cold Go build outside each machine's
// short SSH apply deadline. Builds are shared by target platform.
func prepareCrossBuilds(ctx context.Context, in Input, plans []remotePlan) ([]string, error) {
	built := map[string]string{}
	cleanup := []string{}
	for index := range plans {
		plan := &plans[index]
		if plan.Error != nil || !plan.Install || !isCrossPlatform(plan.Machine) {
			continue
		}
		if strings.TrimSpace(plan.BuildRef) == "" {
			return cleanup, errors.New("remote plan has no usable revision for cross-build")
		}
		key := plan.Machine.OS + "/" + plan.Machine.Arch
		if source := built[key]; source != "" {
			plan.BinarySource = source
			continue
		}
		file, err := os.CreateTemp("", "nexo-cross-build-*")
		if err != nil {
			return cleanup, fmt.Errorf("create cross-build output: %w", err)
		}
		path := file.Name()
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return cleanup, fmt.Errorf("close cross-build output: %w", err)
		}
		buildCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = (&install.LocalBuilder{Runner: in.Runner}).Build(buildCtx, upgrade.DefaultRepository, plan.BuildRef, install.Platform{OS: plan.Machine.OS, Arch: plan.Machine.Arch}, path)
		cancel()
		if err != nil {
			_ = os.Remove(path)
			return cleanup, fmt.Errorf("cross-build nexo for %s/%s: %w", plan.Machine.OS, plan.Machine.Arch, err)
		}
		built[key] = path
		cleanup = append(cleanup, path)
		plan.BinarySource = path
	}
	// The files remain available to the apply goroutines until the caller's
	// deferred cleanup runs.
	return cleanup, nil
}

func applyRemotePlan(ctx context.Context, in Input, options Options, sshConfig string, plan remotePlan) error {
	if plan.Install {
		var source = plan.BinarySource
		if source == "" {
			var err error
			source, err = os.Executable()
			if err != nil {
				return fmt.Errorf("find local nexo binary: %w", err)
			}
		}
		if err := install.CopyAtomic(ctx, install.NewRemoteTarget(in.Runner, plan.Machine.Machine, sshConfig), source, plan.BinaryAt, nil); err != nil {
			return err
		}
	}
	for _, change := range plan.Changes {
		if !change.Changed() {
			continue
		}
		if err := applyRemoteChange(ctx, in.Runner, plan.Machine.Machine, sshConfig, change.Path, change.After); err != nil {
			return err
		}
	}
	if plan.Backend == "tmux" && (plan.Install || len(plan.Changes) > 0 || !plan.Machine.WatcherRunning) {
		path := filepath.Join(defaultRemoteHome(plan.Machine), ".tmux.conf")
		if _, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "tmux", "source-file", path); err != nil {
			return err
		}
		if _, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "nexo", "tmux", "init", "--backend=tmux"); err != nil {
			return err
		}
		if out, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "sh", "-c", "pgrep -f '[n]exo.*agents.*watch' >/dev/null"); err != nil || strings.TrimSpace(string(out)) != "" {
			return errors.New("remote tmux watcher did not become ready")
		}
	}
	if plan.Backend == "herdr" {
		if !plan.Machine.HasHerdrPlugin {
			if _, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "herdr", "plugin", "install", "vieitesss/nexo"); err != nil {
				return err
			}
		}
		if !plan.Machine.HasHerdrPlugin || len(plan.Changes) > 0 {
			if _, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "herdr", "server", "reload-config"); err != nil {
				return err
			}
		}
	}
	if _, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "sh", "-c", "command -v nexo"); err != nil {
		return errors.New("nexo is not visible in non-interactive SSH shell")
	}
	if out, err := run(ctx, in.Runner, plan.Machine.Machine, sshConfig, "sh", "-c", "locale charmap"); err != nil || !strings.Contains(strings.ToUpper(strings.TrimSpace(string(out))), "UTF-8") && !strings.Contains(strings.ToUpper(strings.TrimSpace(string(out))), "UTF8") {
		return errors.New("remote non-interactive shell is not using a UTF-8 locale")
	}
	return nil
}

func probeRemoteMachines(ctx context.Context, in Input, machines []string, sshConfig string) []RemoteMachine {
	facts := make([]RemoteMachine, len(machines))
	var wg sync.WaitGroup
	for index, machine := range machines {
		wg.Add(1)
		go func(index int, machine string) {
			defer wg.Done()
			machineCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			facts[index] = ProbeRemote(machineCtx, in.Runner, machine, sshConfig)
		}(index, machine)
	}
	wg.Wait()
	return facts
}

// ambiguousFromFacts maps each machine running both multiplexers to the answer
// its question starts on: the local backend, so one Enter keeps them alike.
func ambiguousFromFacts(machines []string, facts []RemoteMachine, configured map[string]string, local string) map[string]string {
	suggested := ""
	if local == "tmux" || local == "herdr" {
		suggested = local
	}
	ambiguous := map[string]string{}
	for index, machine := range machines {
		if configuredBackend(configured, machine) != "" || index >= len(facts) {
			continue
		}
		fact := facts[index]
		if fact.Error == nil && fact.TmuxRunning && fact.HerdrRunning {
			ambiguous[machine] = suggested
		}
	}
	return ambiguous
}

func configuredBackend(configured map[string]string, machine string) string {
	if value := strings.ToLower(strings.TrimSpace(configured[machine])); value == "tmux" || value == "herdr" {
		return value
	}
	for alias, value := range configured {
		if strings.EqualFold(alias, machine) {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "tmux" || value == "herdr" {
				return value
			}
		}
	}
	return ""
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

// pathsOrDefaults picks the project roots setup offers: the ones passed on
// the command line, else the ones already configured, else the common roots
// that exist. Configured roots come first so a re-run starts from them
// instead of proposing defaults the config may already cover.
func pathsOrDefaults(paths, configured []string, home string) []string {
	if len(paths) > 0 {
		return paths
	}
	if len(configured) > 0 {
		return append([]string(nil), configured...)
	}
	return DefaultPaths(home, nil)
}

func localRepairNeeded(ctx context.Context, in Input, backendName string) bool {
	if backendName == "herdr" {
		out, err := in.Runner.Run(ctx, "herdr", "plugin", "list")
		return err != nil || !pluginInstalled(string(out))
	}
	_, err := in.Runner.Run(ctx, "sh", "-c", "pgrep -f '[n]exo.*agents.*watch' >/dev/null")
	return err != nil
}

func pluginInstalled(output string) bool {
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "vieitesss/nexo") {
			return true
		}
	}
	return false
}

func activeTmuxConfigLine(contents string, needle string) bool {
	for _, raw := range strings.Split(contents, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func bindingConflicts(ctx context.Context, in Input, home, backendName, tuiKey, agentsKey string) []string {
	keys := []string{}
	if strings.TrimSpace(tuiKey) != "" {
		keys = append(keys, strings.TrimSpace(tuiKey))
	}
	if strings.TrimSpace(agentsKey) != "" {
		keys = append(keys, strings.TrimSpace(agentsKey))
	}
	if len(keys) == 0 {
		return nil
	}
	if backendName == "tmux" {
		out, err := in.Runner.Run(ctx, "tmux", "list-keys")
		if err != nil {
			return nil
		}
		return tmuxBindingConflicts(string(out), keys)
	}
	out, err := os.ReadFile(filepath.Join(home, ".config", "herdr", "config.toml"))
	if err != nil {
		return nil
	}
	text := string(out)
	text = strings.ReplaceAll(text, managedBody(text), "")
	conflicts := []string{}
	for _, key := range keys {
		if strings.Contains(text, key) {
			conflicts = append(conflicts, key)
		}
	}
	return conflicts
}

// tmuxBindingConflicts reports keys that tmux list-keys already binds in the
// same table, ignoring the nexo bindings a previous setup run installed.
func tmuxBindingConflicts(listKeys string, keys []string) []string {
	conflicts := []string{}
	for _, key := range keys {
		for _, line := range strings.Split(listKeys, "\n") {
			if strings.Contains(line, "nexo tui") {
				continue
			}
			if bound, ok := parseTmuxBind(line); ok && sameTmuxKey(bound, key) {
				conflicts = append(conflicts, key)
				break
			}
		}
	}
	return conflicts
}

func existingBindings(home, backendName string) (string, string) {
	path := filepath.Join(home, ".config", "herdr", "config.toml")
	if backendName == "tmux" {
		path = filepath.Join(home, ".tmux.conf")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	return bindingsFromContents(string(contents), backendName)
}

// bindingsFromContents identifies bindings by their nexo action, not by their
// position. This preserves an agents-only block and both bindings on reruns.
func bindingsFromContents(contents, backendName string) (string, string) {
	body := managedBody(contents)
	if backendName == "tmux" {
		var tuiKey, agentsKey string
		for _, raw := range strings.Split(body, "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "#") {
				continue
			}
			key, ok := parseTmuxBind(line)
			if !ok {
				continue
			}
			switch {
			case strings.Contains(line, "nexo tui agents"):
				agentsKey = key
			case strings.Contains(line, "nexo tui"):
				tuiKey = key
			}
		}
		return tuiKey, agentsKey
	}
	var tuiKey, agentsKey string
	for _, block := range strings.Split(body, "[[keys.command]]") {
		var key, command string
		for _, raw := range strings.Split(block, "\n") {
			line := strings.TrimSpace(raw)
			if strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "key = ") {
				key = strings.Trim(strings.TrimPrefix(line, "key = "), "\"")
			}
			if strings.HasPrefix(line, "command = ") {
				command = strings.Trim(strings.TrimPrefix(line, "command = "), "\"")
			}
		}
		switch command {
		case "nexo.tui-agents":
			agentsKey = key
		case "nexo.tui":
			tuiKey = key
		}
	}
	return tuiKey, agentsKey
}

func validateBindings(backendName, tuiKey, agentsKey string) error {
	for label, key := range map[string]string{"TUI": tuiKey, "agents": agentsKey} {
		if err := config.ValidateBindingKeyForBackend(backendName, key); err != nil {
			return fmt.Errorf("%s binding: %w", label, err)
		}
	}
	return nil
}

func managedBody(contents string) string {
	start := strings.Index(contents, ManagedStart)
	if start < 0 {
		return ""
	}
	start += len(ManagedStart)
	end := strings.Index(contents[start:], ManagedEnd)
	if end < 0 {
		return ""
	}
	return strings.Trim(contents[start:start+end], " \r\n")
}

func printChange(w io.Writer, change Change) {
	if diff := FormatDiff(change); diff != "" {
		writeLine(w, "%s", diff)
	}
}

func confirm(input io.Reader, output io.Writer) error {
	writeLine(output, "Apply setup changes? [y/N] ")
	if input == nil {
		return errors.New("setup cannot read confirmation from stdin")
	}
	var answer string
	_, _ = fmt.Fscan(input, &answer)
	if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
		return nil
	}
	return errors.New("setup canceled")
}

func runCheck(ctx context.Context, in Input, options Options, backendName string) error {
	failures := []string{}
	localPathBytes, localPathErr := in.Runner.Run(ctx, "sh", "-c", "command -v nexo")
	localPath := strings.TrimSpace(string(localPathBytes))
	localVersion := ""
	if localPathErr == nil && localPath != "" {
		if out, err := in.Runner.Run(ctx, localPath, "--version"); err == nil {
			localVersion = strings.TrimSpace(string(out))
		}
	}
	configPath := options.ConfigPath
	if configPath == "" {
		configPath = config.DefaultPathFrom(in.Home, "")
	}
	configInfo, configErr := os.Stat(configPath)
	configOK := configErr == nil && !configInfo.IsDir()
	localeOut, localeErr := in.Runner.Run(ctx, "sh", "-c", "locale charmap")
	localeOK := localeErr == nil && isUTF8Locale(string(localeOut))
	focusOK, initOK, watcherOK := false, false, false
	if backendName == "tmux" {
		readiness, readinessErr := probeTmuxReadiness(ctx, in.Runner, "", "")
		focusOK = readinessErr == nil && readiness.TmuxFocusOK
		contents, err := os.ReadFile(filepath.Join(in.Home, ".tmux.conf"))
		configLineOK := err == nil && (activeTmuxConfigLine(string(contents), "nexo tmux init") || activeTmuxConfigLine(string(contents), "@plugin 'vieitesss/nexo'") || activeTmuxConfigLine(string(contents), `@plugin "vieitesss/nexo"`))
		initOK = readinessErr == nil && configLineOK && readiness.TmuxHooksOK
		watcherOK = readinessErr == nil && readiness.WatcherRunning
	} else {
		pluginOut, pluginErr := in.Runner.Run(ctx, "herdr", "plugin", "list")
		statusOut, statusErr := in.Runner.Run(ctx, "herdr", "status", "server", "--json")
		initOK = pluginErr == nil && pluginInstalled(string(pluginOut))
		focusOK = statusErr == nil && herdrServerRunning(string(statusOut))
		watcherOK = focusOK
	}
	writeLine(in.Stdout, "local: binary=%s path=%s version=%s config=%s locale=%s focus=%s init/plugin=%s watcher=%s backend=%s\n", boolString(localPathErr == nil && localPath != ""), localPath, localVersion, boolString(configOK), boolString(localeOK), boolString(focusOK), boolString(initOK), boolString(watcherOK), backendName)
	if localPathErr != nil || localPath == "" || localVersion == "" || !configOK || !localeOK || !focusOK || !initOK || !watcherOK {
		failures = append(failures, "local")
	}
	machines, err := checkMachines(options, in, configPath)
	if err != nil {
		return err
	}
	sshConfig := sshConfigPath(options, in.Home)
	configuredBackends := map[string]string{}
	if existing, loadErr := config.LoadOptional(configPath); loadErr == nil {
		for machine, value := range existing.Remote.Backends {
			configuredBackends[machine] = value
		}
	}
	var wg sync.WaitGroup
	results := make([]remotePlan, len(machines))
	for index, machine := range machines {
		wg.Add(1)
		go func(index int, machine string) {
			defer wg.Done()
			configured := configuredBackend(configuredBackends, machine)
			results[index] = planRemote(ctx, in, options, machine, sshConfig, configured, nil)
		}(index, machine)
	}
	wg.Wait()
	for _, result := range results {
		if result.Error != nil {
			writeLine(in.Stdout, "%s: failed (%v)\n", result.Machine.Machine, result.Error)
			failures = append(failures, result.Machine.Machine)
			continue
		}
		missing := []string{}
		if !result.Machine.HasNexo {
			missing = append(missing, "binary")
		}
		if result.Machine.NexoVersion == "" {
			missing = append(missing, "version")
		}
		if !result.Machine.LocaleUTF8 {
			missing = append(missing, "locale")
		}
		if result.Backend == "" {
			missing = append(missing, "backend")
		}
		fileMissing := make(map[string]bool)
		for _, change := range result.Changes {
			fileMissing[filepath.Base(change.Path)] = true
		}
		focusOK := result.Backend == "herdr" && result.Machine.HerdrRunning || result.Backend == "tmux" && result.Machine.TmuxFocusOK
		initOK := result.Backend == "herdr" && result.Machine.HasHerdrPlugin || result.Backend == "tmux" && !fileMissing[".tmux.conf"] && result.Machine.TmuxHooksOK
		watcherOK := result.Backend == "herdr" || result.Machine.WatcherRunning
		if !focusOK {
			missing = append(missing, "focus")
		}
		if !initOK {
			missing = append(missing, "init/plugin")
		}
		if !watcherOK {
			missing = append(missing, "watcher")
		}
		writeLine(in.Stdout, "%s: binary=%s version=%s locale=%s backend=%s focus=%s init/plugin=%s watcher=%s\n", result.Machine.Machine, boolString(result.Machine.HasNexo), result.Machine.NexoVersion, result.Machine.Locale, result.Backend, boolString(focusOK), boolString(initOK), boolString(watcherOK))
		if len(missing) > 0 {
			failures = append(failures, result.Machine.Machine+" (missing "+strings.Join(missing, ", ")+")")
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("check failed on %s", strings.Join(failures, ", "))
	}
	return nil
}

func checkMachines(options Options, in Input, configPath string) ([]string, error) {
	if len(options.Machines) > 0 || options.LocalOnly {
		return unique(options.Machines), nil
	}
	existing, err := config.LoadOptional(configPath)
	if err != nil {
		return nil, err
	}
	return existing.Remote.ConfiguredMachines(), nil
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

func probeTmuxReadiness(ctx context.Context, commandRunner runner.CommandRunner, machine, sshConfig string) (RemoteMachine, error) {
	out, err := run(ctx, commandRunner, machine, sshConfig, "sh", "-c", tmuxReadinessScript)
	if err != nil {
		return RemoteMachine{}, err
	}
	return parseTmuxReadiness(out), nil
}

func parseTmuxReadiness(output []byte) RemoteMachine {
	result := RemoteMachine{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "watcher":
			result.WatcherRunning = strings.TrimSpace(value) == "yes"
		case "tmux_running":
			result.TmuxRunning = strings.TrimSpace(value) == "yes"
		case "tmux_focus":
			result.TmuxFocusOK = strings.EqualFold(strings.TrimSpace(value), "on")
		case "tmux_hooks":
			result.TmuxHooksOK = strings.TrimSpace(value) == "yes"
		}
	}
	return result
}

func isUTF8Locale(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
}

func herdrServerRunning(value string) bool {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
	return strings.Contains(compact, `"running":true`)
}

func boolString(value bool) string {
	if value {
		return "ok"
	}
	return "missing"
}

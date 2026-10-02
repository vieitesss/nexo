package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDecodeSupportsNestedFeatureSections(t *testing.T) {
	type workingPattern struct {
		Name string `toml:"name"`
	}
	type featureConfig struct {
		Paths          []string       `toml:"paths"`
		Enabled        bool           `toml:"enabled"`
		Retries        int            `toml:"retries"`
		WorkingPattern workingPattern `toml:"working-pattern"`
	}
	type testConfig struct {
		Agents featureConfig `toml:"agents"`
	}

	var got testConfig
	err := decode([]byte(`[agents]
paths = ["~/work/*"]
enabled = true
retries = 3

[agents.working-pattern]
name = "focused"
`), "test.toml", &got)
	if err != nil {
		t.Fatalf("decode() error = %v", err)
	}
	if got.Agents.Paths[0] != "~/work/*" || !got.Agents.Enabled || got.Agents.Retries != 3 || got.Agents.WorkingPattern.Name != "focused" {
		t.Fatalf("decoded config = %#v, want nested feature values", got)
	}
}

func TestDecodeUnknownNestedKeyNamesFullPath(t *testing.T) {
	type agentsConfig struct {
		Paths []string `toml:"paths"`
	}
	type testConfig struct {
		Agents agentsConfig `toml:"agents"`
	}

	var got testConfig
	err := decode([]byte("[agents]\nworking-pattern = true\n"), "test.toml", &got)
	if err == nil || !strings.Contains(err.Error(), "agents.working-pattern") {
		t.Fatalf("decode() error = %v, want full nested key path", err)
	}
}

func TestLoadParsesProjectsAtTopLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("paths = [\"~/work/*\"]\ngit-from-home = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "~/work/*" || !got.GitFromHome {
		t.Fatalf("projects config = %#v, want top-level project settings", got.ProjectsConfig)
	}
}

func TestLoadParsesAgentSettingsAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agents.working-pattern]\naider = \"…\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Agents.Notify == nil || !*got.Agents.Notify || got.Agents.Sound || got.Agents.IdleSeconds != 3 || got.Agents.PollInterval != 2 || len(got.Agents.Processes) != 7 || got.Agents.WorkingPattern["aider"] != "…" || got.Agents.HighlightColor != "red" {
		t.Fatalf("Agents = %#v, want notification defaults and aider pattern", got.Agents)
	}
}

func TestLoadParsesTUITheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[tui]\ntheme = \"light\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.TUI.Theme != ThemeLight {
		t.Fatalf("TUI.Theme = %q, want %q", got.TUI.Theme, ThemeLight)
	}
}

func TestLoadDefaultsTUIThemeToAuto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.TUI.Theme != ThemeAuto {
		t.Fatalf("TUI.Theme = %q, want %q", got.TUI.Theme, ThemeAuto)
	}
}

func TestLoadRejectsInvalidTUITheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[tui]\ntheme = \"system\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "invalid tui theme") || !strings.Contains(err.Error(), "tui.theme") {
		t.Fatalf("Load() error = %v, want tui.theme validation", err)
	}
}

// TestThemeFromConfigFollowsOnlyTheUITheme pins the loader seam the running
// TUI uses to follow theme edits: it validates the theme exactly like a full
// load, defaults an absent value to auto, and reports bad content as an error.
func TestThemeFromConfigFollowsOnlyTheUITheme(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
		want     string
		wantErr  string
	}{
		{name: "explicit light", contents: "[tui]\ntheme = \"light\"\n", want: ThemeLight},
		{name: "explicit dark", contents: "[tui]\ntheme = \"dark\"\n", want: ThemeDark},
		{name: "absent defaults to auto", contents: "backend = \"tmux\"\n", want: ThemeAuto},
		{name: "invalid value", contents: "[tui]\ntheme = \"system\"\n", wantErr: "invalid tui theme"},
		{name: "broken toml", contents: "[tui", wantErr: "config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ThemeFromConfig([]byte(test.contents), "config.toml")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("ThemeFromConfig() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ThemeFromConfig() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ThemeFromConfig() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLoadParsesAgentNotificationSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agents]\nnotify = false\nsound = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Agents.Notify == nil || *got.Agents.Notify || !got.Agents.Sound {
		t.Fatalf("Agents = %#v, want notify=false and sound=true", got.Agents)
	}
}

func TestAgentNotificationDefaultsPreserveProgrammaticFalse(t *testing.T) {
	enabled := false
	got := (AgentsConfig{Notify: &enabled}).WithDefaults()
	if got.Notify == nil || *got.Notify {
		t.Fatalf("Notify = %#v, want explicit false preserved", got.Notify)
	}
}

func TestLoadRejectsUnknownTopLevelProjectKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("project-paths = [\"~/work\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "project-paths") {
		t.Fatalf("Load() error = %v, want unknown top-level key", err)
	}
}

func TestLoadParsesAgentHighlightColor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agents]\nhighlight-color = \"blue\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Agents.HighlightColor != "blue" {
		t.Fatalf("HighlightColor = %q, want blue", got.Agents.HighlightColor)
	}
}

// TestLoadAcceptsHighlightColoursAndStyles pins the two documented forms a
// valid highlight value may take.
func TestLoadAcceptsHighlightColoursAndStyles(t *testing.T) {
	for _, value := range []string{"red", "colour123", "#ff0000", "brightwhite", "bg=red,bold", "fg=colour123,underscore"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[agents]\nhighlight-color = "+strconv.Quote(value)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("Load() error = %v, want %q accepted", err, value)
			}
		})
	}
}

// TestLoadRejectsInvalidHighlightColour pins MUST 3: a bad highlight value is
// rejected at the config boundary instead of killing the watcher on the first
// unseen agent.
func TestLoadRejectsInvalidHighlightColour(t *testing.T) {
	for _, value := range []string{"not-a-colour", "colour256", "#ff00", "bg=not-a-colour,bold", "bold", "bg=red,nope"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[agents]\nhighlight-color = "+strconv.Quote(value)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), highlightColorKey) {
				t.Fatalf("Load() error = %v, want %s validation", err, highlightColorKey)
			}
		})
	}
}

func TestLoadParsesRemoteSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[remote]\nmachines = [\"devbox\", \"gpu1\"]\nssh-config = \"/tmp/ssh-config\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got.Remote, RemoteConfig{Machines: []string{"devbox", "gpu1"}, SSHConfig: "/tmp/ssh-config"}) {
		t.Fatalf("Remote = %#v, want configured machines", got.Remote)
	}
}

func TestLoadExpandsRemoteSSHConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"tilde directory", "~/.ssh/config", filepath.Join(home, ".ssh", "config")},
		{"tilde alone", "~", home},
		{"absolute", "/tmp/ssh-config", "/tmp/ssh-config"},
		{"empty", "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "[remote]\nssh-config = " + strconv.Quote(testCase.value) + "\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.Remote.SSHConfig != testCase.want {
				t.Fatalf("Remote.SSHConfig = %q, want %q", got.Remote.SSHConfig, testCase.want)
			}
		})
	}
}

func TestLoadParsesRemoteBackends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := []byte("[remote]\nmachines = [\"devbox\", \"gpu1\"]\n[remote.backends]\ndevbox = \"tmux\"\ngpu1 = \"herdr\"\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Remote.Backends["devbox"] != "tmux" || got.Remote.Backends["gpu1"] != "herdr" {
		t.Fatalf("remote backends = %#v, want configured per-machine values", got.Remote.Backends)
	}
}

func TestLoadRejectsInvalidRemoteBackends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[remote]\n[remote.backends]\nother = \"tmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want backend-only machine to be accepted", err)
	}
	if !got.Remote.IsMachineConfigured(" OTHER ") {
		t.Fatalf("remote machine set = %#v, want backend-only machine", got.Remote.ConfiguredMachines())
	}

	path = filepath.Join(t.TempDir(), "invalid-backend.toml")
	if err := os.WriteFile(path, []byte("[remote]\nmachines = [\"devbox\"]\n[remote.backends]\ndevbox = \"screen\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path)
	if err == nil || !strings.Contains(err.Error(), "invalid remote backend") || !strings.Contains(err.Error(), "remote.backends") {
		t.Fatalf("Load() error = %v, want invalid backend and remote.backends", err)
	}
}

func TestLoadParsesRemoteProxySettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[remote]\ntransport = \"mosh\"\nreconnect-initial = \"2s\"\nreconnect-max = \"12s\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Remote.Transport != "mosh" || got.Remote.ReconnectInitial != 2*time.Second || got.Remote.ReconnectMax != 12*time.Second {
		t.Fatalf("Remote = %#v, want mosh and durations", got.Remote)
	}
	if got.Remote.EffectiveTransport() != "mosh" {
		t.Fatalf("EffectiveTransport() = %q, want mosh", got.Remote.EffectiveTransport())
	}
}

func TestLoadRejectsInvalidRemoteProxySettings(t *testing.T) {
	for _, contents := range []string{
		"[remote]\ntransport = \"telnet\"\n",
		"[remote]\nreconnect-initial = \"0s\"\n",
		"[remote]\nreconnect-max = \"500ms\"\nreconnect-initial = \"1s\"\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("Load(%q) error = nil, want validation error", contents)
		}
	}
}

func TestLoadRejectsRemoteLocalMachineAliases(t *testing.T) {
	for _, machine := range []string{"local", "-oStrictHostKeyChecking=no"} {
		t.Run(machine, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := []byte("[remote]\nmachines = [\"" + machine + "\"]\n")
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), "remote.machines") || !strings.Contains(err.Error(), machine) {
				t.Fatalf("Load() error = %v, want remote.machines validation for %q", err, machine)
			}
		})
	}
}

func TestLoadRejectsRemoteLocalHostnameAlias(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		t.Skip("hostname unavailable")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[remote]\nmachines = [\""+hostname+"\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path)
	if err == nil || !strings.Contains(err.Error(), "remote.machines") {
		t.Fatalf("Load() error = %v, want hostname alias validation", err)
	}
}

func TestLoadParsesBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("backend = \"tmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Backend != "tmux" {
		t.Fatalf("Backend = %q, want tmux", got.Backend)
	}
}

func TestLoadRejectsInvalidBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("backend = \"screen\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "invalid backend") {
		t.Fatalf("Load() error = %v, want invalid backend error", err)
	}
}

func TestLoadRejectsUnquotedBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("backend = tmux\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want invalid TOML error")
	}
}

func TestLoadUnknownKeyNamesFileAndKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("ui = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want unknown-key error")
	}
	message := err.Error()
	if !strings.Contains(message, path) || !strings.Contains(message, "ui") {
		t.Fatalf("error = %q, want file and key", message)
	}
}

func TestLoadOptionalMissingIsEmpty(t *testing.T) {
	got, err := LoadOptional(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadOptional() error = %v", err)
	}
	if !reflect.DeepEqual(got, Config{}) {
		t.Fatalf("config = %#v, want empty config", got)
	}
}

func TestDefaultPathHonorsXDG(t *testing.T) {
	if got, want := DefaultPathFrom("/home/tester", "/tmp/config"), "/tmp/config/nexo/config.toml"; got != want {
		t.Fatalf("DefaultPathFrom() = %q, want %q", got, want)
	}
	if got, want := DefaultPathFrom("/home/tester", ""), "/home/tester/.config/nexo/config.toml"; got != want {
		t.Fatalf("DefaultPathFrom() = %q, want %q", got, want)
	}
}

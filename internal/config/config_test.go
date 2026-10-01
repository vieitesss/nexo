package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if got.Agents.Notify == nil || !*got.Agents.Notify || got.Agents.Sound || got.Agents.IdleSeconds != 3 || got.Agents.PollInterval != 2 || len(got.Agents.Processes) != 7 || got.Agents.WorkingPattern["aider"] != "…" {
		t.Fatalf("Agents = %#v, want notification defaults and aider pattern", got.Agents)
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
	if got.Remote.BackendFor("devbox") != "tmux" || got.Remote.BackendFor("gpu1") != "herdr" || got.Remote.BackendFor("other") != "auto" {
		t.Fatalf("remote backends = %#v, want per-machine values and auto default", got.Remote.Backends)
	}
}

func TestLoadRejectsInvalidRemoteBackends(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  string
		message string
	}{
		{name: "unknown machine", config: "[remote]\nmachines = [\"devbox\"]\n[remote.backends]\nother = \"tmux\"\n", message: "unknown machine"},
		{name: "unknown value", config: "[remote]\nmachines = [\"devbox\"]\n[remote.backends]\ndevbox = \"screen\"\n", message: "invalid remote backend"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), test.message) || !strings.Contains(err.Error(), "remote.backends") {
				t.Fatalf("Load() error = %v, want %q and remote.backends", err, test.message)
			}
		})
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

func TestLoadRejectsTmuxSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[tmux]\nplace-key = \"none\"\npopup-width = \"70%\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "tmux") {
		t.Fatalf("Load() error = %v, want strict unknown tmux key", err)
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

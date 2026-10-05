package config

import (
	"strings"
	"testing"
)

func TestPrepareSetupPreservesInlineCommentsAndMultilineArrays(t *testing.T) {
	before := "[agents]\nnotify = false # keep this comment\n[remote]\nmachines = [\n  \"old\", # existing\n]\n"
	notify := true
	after, err := PrepareSetup([]byte(before), "config.toml", "/home/u", SetupValues{Notify: &notify, Machines: []string{"new"}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.Contains(text, "notify = true # keep this comment") {
		t.Fatalf("notify comment was not preserved: %q", text)
	}
	if strings.Count(text, "new") != 1 || !strings.Contains(text, "\"old\"") {
		t.Fatalf("machines array was not updated once: %q", text)
	}
	var cfg Config
	if err := decode(after, "config.toml", &cfg); err != nil {
		t.Fatalf("result is not valid TOML: %v\n%s", err, text)
	}
	second, err := PrepareSetup(after, "config.toml", "/home/u", SetupValues{Notify: &notify, Machines: []string{"new"}})
	if err != nil || string(second) != text {
		t.Fatalf("second plan changed content: err=%v\n%s", err, second)
	}
}

func TestPrepareSetupQuotesDottedBackendKeys(t *testing.T) {
	after, err := PrepareSetup(nil, "config.toml", "/home/u", SetupValues{Backends: map[string]string{"dev.box": "tmux"}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.Contains(text, `"dev.box" = "tmux"`) {
		t.Fatalf("dotted key was not quoted: %q", text)
	}
	again, err := PrepareSetup(after, "config.toml", "/home/u", SetupValues{Backends: map[string]string{"dev.box": "tmux"}})
	if err != nil || string(again) != text {
		t.Fatalf("dotted key was duplicated on rerun: err=%v\n%s", err, again)
	}
}

func TestPrepareSetupDoesNotWrite(t *testing.T) {
	before := []byte("[agents]\nnotify = false\n")
	copyBefore := append([]byte(nil), before...)
	notify := true
	if _, err := PrepareSetup(before, "not-a-real-file", "/home/u", SetupValues{Notify: &notify}); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(copyBefore) {
		t.Fatal("planning mutated input bytes")
	}
}

func TestMergeAgentsPreservesRemoteTables(t *testing.T) {
	local := []byte("[agents]\nnotify = true\nprocesses = [\"pi\"]\n")
	remote := []byte("backend = \"tmux\"\n[remote]\nmachines = [\"keep\"]\n")
	got, err := MergeAgents(remote, local, "remote.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, `backend = "tmux"`) || !strings.Contains(text, `machines = ["keep"]`) || !strings.Contains(text, `processes = ["pi"]`) {
		t.Fatalf("merge discarded unrelated values: %q", text)
	}
	var cfg Config
	if err := decode(got, "remote.toml", &cfg); err != nil {
		t.Fatalf("merged config invalid: %v", err)
	}
}

func TestPrepareSetupMergesDottedKeys(t *testing.T) {
	notify := true
	got, err := PrepareSetup([]byte("backend = \"tmux\"\nagents.notify = false\n"), "config.toml", "/home/u", SetupValues{Notify: &notify})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "agents.notify = true") {
		t.Fatalf("dotted key was not updated: %s", got)
	}
}

func TestMergeAgentsSupportsDottedAndInlineTables(t *testing.T) {
	tests := []struct {
		name   string
		local  string
		remote string
		want   string
	}{
		{name: "dotted", local: "agents.notify = true\nagents.processes = [\"pi\"]\n", remote: "backend = \"tmux\"\n", want: "notify = true"},
		{name: "inline", local: "agents = { notify = true, processes = [\"pi\"] }\n", remote: "backend = \"tmux\"\n[agents]\nnotify = false\n", want: "notify = true"},
		{name: "remote-inline", local: "[agents]\nnotify = true\n", remote: "agents = { notify = false }\n", want: "notify = true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := MergeAgents([]byte(test.remote), []byte(test.local), "remote.toml")
			if err != nil {
				t.Logf("partial merged config = %q", got)
				t.Fatal(err)
			}
			if !strings.Contains(string(got), test.want) {
				t.Fatalf("merged config = %q, want %q", got, test.want)
			}
			var cfg Config
			if err := decode(got, "remote.toml", &cfg); err != nil {
				t.Fatalf("result is invalid TOML: %v\n%s", err, got)
			}
		})
	}
}

func TestPrepareSetupAddsFirstValueToEmptyInlineTable(t *testing.T) {
	got, err := PrepareSetup([]byte("backend = \"tmux\"\nagents = {}\n"), "config.toml", "/home/u", SetupValues{Notify: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "agents = {notify = true}") {
		t.Fatalf("empty inline table was not updated: %s", got)
	}
}

func TestPrepareSetupRefusesExoticInlineShapes(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		want string
	}{
		{name: "nested inline", text: "remote = { machines = [\"old\"], backends = { old = \"tmux\" } }\n", want: "nested inline tables"},
		{name: "table-local dotted", text: "[remote]\nmachines = [\"dev\"]\nbackends.dev = \"tmux\"\n", want: "table-local dotted keys"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := PrepareSetup([]byte(test.text), "config.toml", "/home/u", SetupValues{Backends: map[string]string{"dev": "tmux"}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func boolPtr(value bool) *bool { return &value }

func TestPrepareSetupArrayMembershipUsesDecodedValues(t *testing.T) {
	before := "[remote]\nmachines = [ # \"dev\" is only a suggestion\n  'old',\n]\n"
	after, err := PrepareSetup([]byte(before), "config.toml", "/home/u", SetupValues{Machines: []string{"dev"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "'old'") || !strings.Contains(string(after), `"dev"`) {
		t.Fatalf("machine was not added semantically: %s", after)
	}
}

func TestPrepareSetupSkipsPathsTheConfigAlreadyCovers(t *testing.T) {
	home := t.TempDir()
	before := "paths = [\"~/src\", \"~/work/*\"]\n"
	after, err := PrepareSetup([]byte(before), "config.toml", home, SetupValues{Paths: []string{"~/src", "~/work", home + "/src", "~/code"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "paths = [\"~/src\", \"~/work/*\", \"~/code\"]\n"; string(after) != want {
		t.Fatalf("paths = %q, want only ~/code appended: %q", after, want)
	}
}

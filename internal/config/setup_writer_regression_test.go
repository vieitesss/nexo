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

func TestPrepareSetupPersistsBackendBeforeTables(t *testing.T) {
	after, err := PrepareSetup([]byte("[agents]\nnotify = true\n"), "config.toml", "/home/u", SetupValues{Backend: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.HasPrefix(text, "backend = \"tmux\"\n") {
		t.Fatalf("backend was inserted in a table: %q", text)
	}
	var cfg Config
	if err := decode(after, "config.toml", &cfg); err != nil || cfg.Backend != "tmux" {
		t.Fatalf("backend did not decode: %v %#v", err, cfg)
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

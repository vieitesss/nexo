package containerstate

import (
	"encoding/json"
	"os"
	"testing"
)

func TestStorePersistsLastContainersAndSeparatesProxyHistory(t *testing.T) {
	store := New(t.TempDir(), "tmux", "workstation")
	local := Entry{ID: "$1", Name: "api", Machine: "workstation"}
	proxy := Entry{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}
	if err := store.Record(local); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(proxy); err != nil {
		t.Fatal(err)
	}

	reloaded := New(store.Dir, "tmux", "workstation")
	got, ok, err := reloaded.Previous(false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != local {
		t.Fatalf("local previous = %#v, ok=%v; want %#v", got, ok, local)
	}
	got, ok, err = reloaded.Previous(true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != proxy {
		t.Fatalf("proxy previous = %#v, ok=%v; want %#v", got, ok, proxy)
	}
}

func readDocument(t *testing.T, store Store) document {
	t.Helper()
	contents, err := os.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	var doc document
	if err := json.Unmarshal(contents, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestStoreRecordMakesRepeatedFocusOneHistoryEntry(t *testing.T) {
	store := New(t.TempDir(), "herdr", "workstation")
	entry := Entry{ID: "w1", Name: "api", Machine: "workstation"}
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	if len(readDocument(t, store).Entries) != 1 {
		t.Fatalf("entries = %#v, want one repeated identity", readDocument(t, store).Entries)
	}
}

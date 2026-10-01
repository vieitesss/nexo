package containerstate

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestStorePersistsLastContainersAndSeparatesProxyHistory(t *testing.T) {
	store := New(t.TempDir(), "tmux", "workstation", "/tmp/tmux-1000/default")
	local := Entry{ID: "$1", Name: "api", Machine: "workstation"}
	proxy := Entry{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}
	if err := store.Record(local); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(proxy); err != nil {
		t.Fatal(err)
	}

	reloaded := New(store.Dir, "tmux", "workstation", "/tmp/tmux-1000/default")
	entries, err := reloaded.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != proxy || entries[1] != local {
		t.Fatalf("entries = %#v, want newest proxy then local", entries)
	}
}

func TestStoreNamespacesByNativeServer(t *testing.T) {
	dir := t.TempDir()
	first := New(dir, "tmux", "workstation", "/tmp/tmux-1000/a")
	second := New(dir, "tmux", "workstation", "/tmp/tmux-1000/b")
	if err := first.Record(Entry{ID: "$0", Name: "a", Machine: "workstation"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Record(Entry{ID: "$0", Name: "b", Machine: "workstation"}); err != nil {
		t.Fatal(err)
	}
	firstEntries, err := first.Entries()
	if err != nil {
		t.Fatal(err)
	}
	secondEntries, err := second.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(firstEntries) != 1 || firstEntries[0].Name != "a" {
		t.Fatalf("first server entries = %#v, want only a", firstEntries)
	}
	if len(secondEntries) != 1 || secondEntries[0].Name != "b" {
		t.Fatalf("second server entries = %#v, want only b", secondEntries)
	}
}

func TestStoreDeduplicatesByStableIdentityNotDisplayName(t *testing.T) {
	store := New(t.TempDir(), "tmux", "workstation", "socket")
	if err := store.Record(Entry{ID: "$1", Name: "old-name", Machine: "workstation"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(Entry{ID: "$1", Name: "new-name", Machine: "workstation"}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "new-name" {
		t.Fatalf("entries = %#v, want one refreshed local identity", entries)
	}

	if err := store.Record(Entry{ID: "$2", Name: "api", Machine: "devbox", Proxy: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(Entry{ID: "$9", Name: "api", Machine: "devbox", Proxy: true}); err != nil {
		t.Fatal(err)
	}
	entries, err = store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != "$9" {
		t.Fatalf("entries = %#v, want one proxy identity with the newest native id", entries)
	}
}

func TestStoreKeepsUnchangedTopIdentityWithoutRewriting(t *testing.T) {
	store := New(t.TempDir(), "tmux", "workstation", "socket")
	entry := Entry{ID: "$1", Name: "api", Machine: "workstation"}
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	before := statModTime(t, store.path())
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	if after := statModTime(t, store.path()); !after.Equal(before) {
		t.Fatalf("unchanged top identity rewrote the state file")
	}
}

func TestStoreRetainsLatestIdentityPerMachineBeyondCap(t *testing.T) {
	store := New(t.TempDir(), "tmux", "workstation", "socket")
	if err := store.Record(Entry{ID: "$1", Name: "api", Machine: "devbox", Proxy: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxEntries+8; i++ {
		if err := store.Record(Entry{ID: "$" + strconv.Itoa(100+i), Name: "local", Machine: "workstation"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Proxy && entry.Machine == "devbox" && entry.Name == "api" {
			found = true
		}
	}
	if !found {
		t.Fatalf("devbox last-used identity was evicted: %#v", entries)
	}
	if len(entries) > maxEntries {
		t.Fatalf("entries = %d, want the bounded list to stay at or below the cap", len(entries))
	}
}

func statModTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

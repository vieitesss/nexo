// Package containerstate stores durable recent container identities.
package containerstate

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/statefile"
)

// maxEntries bounds the recent-container list. The latest identity of every
// machine is retained beyond the bound so activity on one machine cannot erase
// another machine's last-used container.
const maxEntries = 32

// Entry is the stable identity of a recently focused or opened container.
// Proxy entries retain the remote machine separately from the local proxy's
// native ID, so reconnects and remote session switches cannot change their
// machine history. Names are mutable display data for local entries and empty
// for newly recorded machine proxies; legacy proxy names remain readable.
type Entry struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Machine string `json:"machine"`
	Proxy   bool   `json:"proxy,omitempty"`
	// RemoteKind is the multiplexer a machine proxy's pane attaches to. It is
	// empty for local entries and for entries written before nexo recorded it;
	// readers treat an empty kind as tmux.
	RemoteKind string `json:"remote_kind,omitempty"`
}

type document struct {
	Entries []Entry `json:"entries"`
}

// Store persists recent identities for one backend on one native server of one
// machine. The native server/socket identity is part of the state namespace,
// because native IDs are only unique within one server instance.
type Store struct {
	Dir     string
	backend string
	machine string
	server  string
}

// LocalMachine returns the stable machine identity used by container history.
func LocalMachine() string {
	machine, err := os.Hostname()
	if err != nil || strings.TrimSpace(machine) == "" {
		return "local"
	}
	return strings.TrimSpace(machine)
}

// New creates a store. dir may be empty to use nexo's default state directory;
// machine is the owning machine identity and server is the native
// server/socket identity that scopes otherwise server-local native IDs.
func New(dir, backend, machine, server string) Store {
	machine = strings.TrimSpace(machine)
	if machine == "" {
		machine = "local"
	}
	server = strings.TrimSpace(server)
	if server == "" {
		server = "default"
	}
	return Store{Dir: dir, backend: strings.TrimSpace(backend), machine: machine, server: server}
}

func (s Store) namespace() string {
	parts := []string{"containers", s.backend, s.machine}
	if s.server != "" {
		parts = append(parts, s.server)
	}
	return strings.Join(parts, "-")
}

func (s Store) path() string {
	return statefile.Path(s.Dir, s.namespace(), "json")
}

func (s Store) lockPath() string {
	return statefile.Path(s.Dir, s.namespace(), "lock")
}

// Record makes entry the most recently used identity. Repeated focus events
// for an unchanged canonical top identity are not written, so a synchronous
// focus hook stays cheap. Local containers deduplicate by native ID and
// proxies by canonical machine/container identity.
func (s Store) Record(entry Entry) error {
	entry.Machine = strings.TrimSpace(entry.Machine)
	if entry.Machine == "" {
		entry.Machine = s.machine
	}
	release, err := statefile.Lock(s.lockPath(), false)
	if err != nil {
		return err
	}
	defer release()
	doc, err := s.read()
	if err != nil {
		return err
	}
	entry = keepKnownRemoteKind(entry, doc.Entries)
	if len(doc.Entries) > 0 && doc.Entries[0] == entry {
		return nil
	}
	entries := make([]Entry, 0, len(doc.Entries)+1)
	entries = append(entries, entry)
	for _, current := range doc.Entries {
		if sameContainer(current, entry) {
			continue
		}
		entries = append(entries, current)
	}
	return s.write(document{Entries: trimEntries(entries, maxEntries)})
}

// Entries returns the recorded identities newest first. The caller selects
// from them against live state; the store never performs native lookups.
func (s Store) Entries() ([]Entry, error) {
	doc, err := s.read()
	if err != nil {
		return nil, err
	}
	return append([]Entry(nil), doc.Entries...), nil
}

// keepKnownRemoteKind keeps the kind of a machine proxy that is recorded
// again without one. Focus events re-record proxies from multiplexer metadata,
// which cannot say which remote multiplexer the pane attaches to, and a proxy
// cannot change its kind without being recreated.
func keepKnownRemoteKind(entry Entry, entries []Entry) Entry {
	if !entry.Proxy || entry.RemoteKind != "" {
		return entry
	}
	for _, current := range entries {
		if sameContainer(current, entry) && current.RemoteKind != "" {
			entry.RemoteKind = current.RemoteKind
			return entry
		}
	}
	return entry
}

func sameContainer(a, b Entry) bool {
	if a.Proxy != b.Proxy {
		return false
	}
	if a.Proxy {
		return strings.EqualFold(strings.TrimSpace(a.Machine), strings.TrimSpace(b.Machine)) && a.Name == b.Name
	}
	return a.ID == b.ID
}

// trimEntries drops the oldest entries beyond max, but never the latest
// identity of a machine.
func trimEntries(entries []Entry, max int) []Entry {
	for len(entries) > max {
		index := lastEvictable(entries)
		if index < 0 {
			break
		}
		entries = append(entries[:index], entries[index+1:]...)
	}
	return entries
}

// lastEvictable returns the oldest entry that is not the latest identity for
// its machine, or -1 when every entry is protected.
func lastEvictable(entries []Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		latest := true
		for j := 0; j < i; j++ {
			if entries[j].Machine == entries[i].Machine {
				latest = false
				break
			}
		}
		if !latest {
			return i
		}
	}
	return -1
}

func (s Store) read() (document, error) {
	contents, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return document{}, nil
	}
	if err != nil {
		return document{}, err
	}
	var doc document
	if err := json.Unmarshal(contents, &doc); err != nil {
		return document{}, err
	}
	return doc, nil
}

func (s Store) write(doc document) error {
	contents, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return statefile.WriteAtomic(s.path(), contents)
}

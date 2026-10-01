// Package containerstate stores durable recent container identities.
package containerstate

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/statefile"
)

const maxEntries = 32

// Entry is the stable identity of a recently focused or opened container.
// Proxy entries retain the remote machine/container pair separately from the
// local proxy's native ID, so reconnects cannot change their target.
type Entry struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Machine string `json:"machine"`
	Proxy   bool   `json:"proxy,omitempty"`
}

type document struct {
	Entries []Entry `json:"entries"`
}

// Store persists recent identities for one backend on one machine.
type Store struct {
	Dir     string
	backend string
	machine string
}

// LocalMachine returns the stable machine identity used by container history.
func LocalMachine() string {
	machine, err := os.Hostname()
	if err != nil || strings.TrimSpace(machine) == "" {
		return "local"
	}
	return strings.TrimSpace(machine)
}

// New creates a store. dir may be empty to use nexo's default state
// directory; machine is the owning machine identity used to isolate records.
func New(dir, backend, machine string) Store {
	machine = strings.TrimSpace(machine)
	if machine == "" {
		machine = "local"
	}
	return Store{Dir: dir, backend: strings.TrimSpace(backend), machine: machine}
}

func (s Store) path() string {
	server := "containers-" + s.backend + "-" + s.machine
	return statefile.Path(s.Dir, server, "json")
}

func (s Store) lockPath() string {
	server := "containers-" + s.backend + "-" + s.machine
	return statefile.Path(s.Dir, server, "lock")
}

// Record makes entry the most recently used identity. Repeated focus events
// move one identity to the front rather than growing the history indefinitely.
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
	filtered := doc.Entries[:0]
	for _, current := range doc.Entries {
		if sameContainer(current, entry) {
			continue
		}
		filtered = append(filtered, current)
	}
	doc.Entries = append([]Entry{entry}, filtered...)
	if len(doc.Entries) > maxEntries {
		doc.Entries = doc.Entries[:maxEntries]
	}
	return s.write(doc)
}

// Previous returns the newest proxy or non-proxy entry as requested. Missing
// history is represented by ok=false and is not an error.
func (s Store) Previous(proxy bool) (entry Entry, ok bool, err error) {
	doc, err := s.read()
	if err != nil {
		return Entry{}, false, err
	}
	for _, current := range doc.Entries {
		if current.Proxy == proxy {
			return current, true, nil
		}
	}
	return Entry{}, false, nil
}

func sameContainer(a, b Entry) bool {
	return a.Proxy == b.Proxy && a.Machine == b.Machine && a.ID == b.ID && a.Name == b.Name
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

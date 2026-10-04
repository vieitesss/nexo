// Package proxyidentity owns the backend-neutral proxy identity and lookup
// policy. Native adapters provide candidate metadata and retain their native
// identity validation, storage, and adoption operations at this module's seam.
package proxyidentity

import (
	"context"
	"errors"
	"strings"
)

// Identity is the conceptual machine/container pair a proxy attaches to.
// A machine-only identity (Container empty) identifies the one proxy for a
// machine. Machine comparisons are case-insensitive; Container remains native
// so an adapter can apply its own canonicalization before crossing this seam.
type Identity struct {
	Machine   string
	Container string
}

// NewIdentity creates the normalized form used in proxy metadata and lookup.
func NewIdentity(machine, container string) Identity {
	return Identity{
		Machine:   NormalizeMachineName(machine),
		Container: strings.TrimSpace(container),
	}
}

// String returns the stable machine-only or MACHINE/CONTAINER representation.
func (i Identity) String() string {
	if strings.TrimSpace(i.Container) == "" {
		return i.Machine
	}
	return i.Machine + "/" + i.Container
}

// Equal compares two identities using the proxy identity contract.
func (i Identity) Equal(other Identity) bool {
	return NormalizeMachineName(i.Machine) == NormalizeMachineName(other.Machine) && i.Container == other.Container
}

// Matches reports whether value is a well-formed identity equal to i.
func (i Identity) Matches(value string) bool {
	other, ok := ParseValue(value)
	return ok && i.Equal(other)
}

// ParseTarget parses a user-facing machine or MACHINE/CONTAINER target.
// Source spelling is retained so callers can pass native values to the
// selected adapter. Machine-only targets are used by proxy close and history.
func ParseTarget(target string) (Identity, error) {
	identity, ok := ParseValue(target)
	if !ok {
		return Identity{}, errors.New("remote target must be MACHINE or MACHINE/CONTAINER")
	}
	if strings.HasPrefix(identity.Machine, "-") {
		return Identity{}, errors.New("remote target has an invalid machine")
	}
	return identity, nil
}

// ParseValue parses metadata or a target without applying native validation.
// It accepts machine-only metadata and is deliberately tolerant of extra
// separators: the first slash separates the machine and the container, whose
// native adapter may permit slashes.
func ParseValue(value string) (Identity, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Identity{}, false
	}
	separator := strings.IndexByte(value, '/')
	if separator < 0 {
		return Identity{Machine: value}, true
	}
	if separator == 0 || separator == len(value)-1 {
		return Identity{}, false
	}
	machine := strings.TrimSpace(value[:separator])
	container := strings.TrimSpace(value[separator+1:])
	if machine == "" || container == "" {
		return Identity{}, false
	}
	return Identity{Machine: machine, Container: container}, true
}

// NormalizeMachineName returns the comparison form for machine names.
func NormalizeMachineName(machine string) string {
	return strings.ToLower(strings.TrimSpace(machine))
}

// MachineSet is the configured-machine policy used by proxy lookup. Its zero
// value is an empty set.
type MachineSet struct {
	names map[string]struct{}
}

// NewMachineSet creates a configured-machine policy from remote names.
func NewMachineSet(machines []string) MachineSet {
	set := MachineSet{names: make(map[string]struct{}, len(machines))}
	for _, machine := range machines {
		if normalized := NormalizeMachineName(machine); normalized != "" {
			set.names[normalized] = struct{}{}
		}
	}
	return set
}

// Contains reports whether machine is configured, matching case and outer
// whitespace as the remote configuration does.
func (s MachineSet) Contains(machine string) bool {
	_, ok := s.names[NormalizeMachineName(machine)]
	return ok && NormalizeMachineName(machine) != ""
}

// Candidate is one native container that may be a proxy. TaggedIdentity is
// authoritative; Label is display data only and never establishes identity.
type Candidate struct {
	ID             string
	TaggedIdentity string
	Label          string
}

// Store is the native adapter seam for proxy lookup. List must return all
// local containers visible to the backend.
type Store interface {
	ListProxyCandidates(context.Context) ([]Candidate, error)
}

// Resolve finds a tagged exact identity. Display labels are never adopted:
// only explicit proxy metadata establishes identity, so a local container named
// like a machine cannot be hijacked. It never performs a remote operation, so
// callers can use it for local-only close resolution while the remote is down.
func Resolve(ctx context.Context, target Identity, store Store) (string, error) {
	if store == nil {
		return "", errors.New("proxy identity requires a store")
	}
	target = NewIdentity(target.Machine, target.Container)
	candidates, err := store.ListProxyCandidates(ctx)
	if err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.TaggedIdentity) != "" && target.Matches(candidate.TaggedIdentity) {
			return candidate.ID, nil
		}
	}
	return "", nil
}

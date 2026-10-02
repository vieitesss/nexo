package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vieitesss/nexo/internal/proxyidentity"
)

// RemoteConfig controls the machines queried by remote place sources.
type RemoteConfig struct {
	Machines         []string          `toml:"machines"`
	Backends         map[string]string `toml:"backends"`
	SSHConfig        string            `toml:"ssh-config"`
	Transport        string            `toml:"transport"`
	ReconnectInitial time.Duration     `toml:"reconnect-initial"`
	ReconnectMax     time.Duration     `toml:"reconnect-max"`
}

const (
	DefaultRemoteTransport  = "ssh"
	DefaultReconnectInitial = time.Second
	DefaultReconnectMax     = 30 * time.Second
)

// EffectiveTransport returns the configured proxy transport, defaulting to
// SSH. Listing and uploads remain SSH operations regardless of this value.
func (r RemoteConfig) EffectiveTransport() string {
	transport := strings.ToLower(strings.TrimSpace(r.Transport))
	if transport == "" {
		return DefaultRemoteTransport
	}
	return transport
}

// ConfiguredMachines returns the configured remote machines, including names
// that occur only as keys in remote.backends. The first spelling wins while
// matching remains case-insensitive and whitespace-tolerant.
func (r RemoteConfig) ConfiguredMachines() []string {
	machines := make([]string, 0, len(r.Machines)+len(r.Backends))
	seen := make(map[string]bool, len(r.Machines)+len(r.Backends))
	add := func(machine string) {
		machine = strings.TrimSpace(machine)
		key := proxyidentity.NormalizeMachineName(machine)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		machines = append(machines, machine)
	}
	for _, machine := range r.Machines {
		add(machine)
	}
	backendMachines := make([]string, 0, len(r.Backends))
	for machine := range r.Backends {
		backendMachines = append(backendMachines, machine)
	}
	sort.Strings(backendMachines)
	for _, machine := range backendMachines {
		add(machine)
	}
	return machines
}

// IsMachineConfigured reports whether machine is named by remote.machines or
// remote.backends.
func (r RemoteConfig) IsMachineConfigured(machine string) bool {
	key := proxyidentity.NormalizeMachineName(machine)
	if key == "" {
		return false
	}
	for _, configured := range r.ConfiguredMachines() {
		if proxyidentity.NormalizeMachineName(configured) == key {
			return true
		}
	}
	return false
}

// ReconnectDelays returns the configured proxy retry bounds with defaults.
func (r RemoteConfig) ReconnectDelays() (time.Duration, time.Duration) {
	initial, maximum := r.ReconnectInitial, r.ReconnectMax
	if initial <= 0 {
		initial = DefaultReconnectInitial
	}
	if maximum <= 0 {
		maximum = DefaultReconnectMax
	}
	return initial, maximum
}

func remoteValidationKey(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "transport"):
		return "remote.transport"
	case strings.Contains(message, "backend"):
		return "remote.backends"
	case strings.Contains(message, "reconnect-initial"):
		return "remote.reconnect-initial"
	case strings.Contains(message, "reconnect-max"):
		return "remote.reconnect-max"
	default:
		return "remote.machines"
	}
}

func validateRemote(remote RemoteConfig) error {
	localMachine, _ := os.Hostname()
	configured := make(map[string]bool, len(remote.Machines)+len(remote.Backends))
	for _, machine := range remote.ConfiguredMachines() {
		trimmed := strings.TrimSpace(machine)
		configured[proxyidentity.NormalizeMachineName(trimmed)] = true
		if strings.HasPrefix(trimmed, "-") {
			return fmt.Errorf("machine %q starts with '-' and would be interpreted as an ssh option", machine)
		}
		if proxyidentity.NormalizeMachineName(trimmed) == "local" || (localMachine != "" && proxyidentity.NormalizeMachineName(trimmed) == proxyidentity.NormalizeMachineName(localMachine)) {
			return fmt.Errorf("machine alias %q refers to the local machine", machine)
		}
	}
	for machine, value := range remote.Backends {
		if !configured[proxyidentity.NormalizeMachineName(machine)] {
			return fmt.Errorf("backend is configured for unknown machine %q", machine)
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "auto", "tmux", "herdr":
		default:
			return fmt.Errorf("invalid remote backend %q for machine %q (want auto, tmux, or herdr)", value, machine)
		}
	}
	if remote.Transport != "" {
		switch strings.ToLower(strings.TrimSpace(remote.Transport)) {
		case "ssh", "mosh", "et":
		default:
			return fmt.Errorf("invalid transport %q (want ssh, mosh, or et)", remote.Transport)
		}
	}
	initial, maximum := remote.ReconnectDelays()
	if initial <= 0 {
		return fmt.Errorf("reconnect-initial must be greater than zero")
	}
	if maximum <= 0 {
		return fmt.Errorf("reconnect-max must be greater than zero")
	}
	if initial > maximum {
		return fmt.Errorf("reconnect-initial must not exceed reconnect-max")
	}
	return nil
}

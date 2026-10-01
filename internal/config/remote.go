package config

import (
	"fmt"
	"os"
	"strings"
	"time"
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
	DefaultRemoteBackend    = "auto"
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

// BackendFor returns the configured remote backend, defaulting to auto.
func (r RemoteConfig) BackendFor(machine string) string {
	machine = strings.TrimSpace(machine)
	backend := strings.TrimSpace(r.Backends[machine])
	if backend == "" {
		for configured, value := range r.Backends {
			if strings.TrimSpace(configured) == machine {
				backend = value
				break
			}
		}
	}
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		return DefaultRemoteBackend
	}
	return backend
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
	configured := make(map[string]bool, len(remote.Machines))
	for _, machine := range remote.Machines {
		trimmed := strings.TrimSpace(machine)
		configured[trimmed] = true
		if strings.HasPrefix(trimmed, "-") {
			return fmt.Errorf("machine %q starts with '-' and would be interpreted as an ssh option", machine)
		}
		if trimmed == "local" || (localMachine != "" && trimmed == localMachine) {
			return fmt.Errorf("machine alias %q refers to the local machine", machine)
		}
	}
	for machine, value := range remote.Backends {
		if !configured[strings.TrimSpace(machine)] {
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

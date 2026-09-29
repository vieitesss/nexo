package config

import (
	"fmt"
	"os"
	"strings"
)

// RemoteConfig controls the machines queried by remote place sources.
type RemoteConfig struct {
	Machines  []string `toml:"machines"`
	SSHConfig string   `toml:"ssh-config"`
}

func validateRemote(remote RemoteConfig) error {
	localMachine, _ := os.Hostname()
	for _, machine := range remote.Machines {
		trimmed := strings.TrimSpace(machine)
		if strings.HasPrefix(trimmed, "-") {
			return fmt.Errorf("machine %q starts with '-' and would be interpreted as an ssh option", machine)
		}
		if trimmed == "local" || (localMachine != "" && trimmed == localMachine) {
			return fmt.Errorf("machine alias %q refers to the local machine", machine)
		}
	}
	return nil
}

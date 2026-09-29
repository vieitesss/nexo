package config

// RemoteConfig controls the machines queried by remote place sources.
type RemoteConfig struct {
	Machines  []string `toml:"machines"`
	SSHConfig string   `toml:"ssh-config"`
}

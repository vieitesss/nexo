package config

// SessionConfig controls generated container names when a template is configured.
type SessionConfig struct {
	NameTemplate *string `toml:"name-template"`
}

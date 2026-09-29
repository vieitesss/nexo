package config

// ProjectsConfig controls project candidate discovery.
type ProjectsConfig struct {
	Paths       []string `toml:"paths"`
	GitFromHome bool     `toml:"git-from-home"`
}

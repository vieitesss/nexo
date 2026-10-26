package config

import "fmt"

func validateBackend(value string) error {
	if value == "" || value == "auto" || value == "tmux" || value == "herdr" || value == "rex" {
		return nil
	}
	return fmt.Errorf("invalid backend %q", value)
}

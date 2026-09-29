package config

import "fmt"

func validateBackend(value string) error {
	if value == "" || value == "auto" || value == "tmux" || value == "herdr" {
		return nil
	}
	return fmt.Errorf("invalid backend %q", value)
}

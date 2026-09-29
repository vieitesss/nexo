package proxy

import (
	"errors"
	"strings"
	"unicode"
)

// AttachCommand builds the shell command typed into a proxy pane. The target
// arguments are included for tmux proxies; Herdr proxies pass an empty target
// and read it from the workspace environment instead.
func AttachCommand(binary, backend, configPath, machine, container string, fromEnv bool) (string, error) {
	values := []string{binary, configPath}
	if !fromEnv {
		values = append(values, machine, container)
	}
	for _, value := range values {
		if err := rejectControl(value); err != nil {
			return "", err
		}
	}
	parts := []string{"exec", shellQuote(binary), "--backend", backend}
	if configPath != "" {
		parts = append(parts, "--config-file", shellQuote(configPath))
	}
	parts = append(parts, "remote", "attach")
	if fromEnv {
		parts = append(parts, "--from-env")
	} else {
		parts = append(parts, "--", shellQuote(machine), shellQuote(container))
	}
	return strings.Join(parts, " "), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func rejectControl(value string) error {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("proxy command values cannot contain control or format characters")
		}
	}
	return nil
}

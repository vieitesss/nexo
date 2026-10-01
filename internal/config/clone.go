package config

import "fmt"

// CloneConfig controls the source form used by the Projects tab.
type CloneConfig struct {
	DefaultProtocol string `toml:"default-protocol"`
	SSHPrefix       string `toml:"ssh-prefix"`
	HTTPSPrefix     string `toml:"https-prefix"`
}

const (
	CloneProtocolSSH   = "ssh"
	CloneProtocolHTTPS = "https"
	DefaultSSHPrefix   = "git@github.com:"
	DefaultHTTPSPrefix = "https://github.com/"
)

// WithDefaults fills the documented clone defaults. It also keeps a
// programmatically-created config useful to callers that do not load TOML.
func (c CloneConfig) WithDefaults() CloneConfig {
	if c.DefaultProtocol == "" {
		c.DefaultProtocol = CloneProtocolSSH
	}
	if c.SSHPrefix == "" {
		c.SSHPrefix = DefaultSSHPrefix
	}
	if c.HTTPSPrefix == "" {
		c.HTTPSPrefix = DefaultHTTPSPrefix
	}
	return c
}

// EffectiveProtocol returns the protocol used when the source form opens.
func (c CloneConfig) EffectiveProtocol() string { return c.WithDefaults().DefaultProtocol }

// Prefix returns the configured prefix for protocol, or an empty string for
// an unsupported protocol.
func (c CloneConfig) Prefix(protocol string) string {
	c = c.WithDefaults()
	switch protocol {
	case CloneProtocolSSH:
		return c.SSHPrefix
	case CloneProtocolHTTPS:
		return c.HTTPSPrefix
	default:
		return ""
	}
}

func validateClone(c CloneConfig) error {
	if c.DefaultProtocol != "" && c.DefaultProtocol != CloneProtocolSSH && c.DefaultProtocol != CloneProtocolHTTPS {
		return fmt.Errorf("invalid clone default protocol %q", c.DefaultProtocol)
	}
	return nil
}

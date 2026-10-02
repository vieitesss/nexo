package capabilities

import "github.com/vieitesss/nexo/internal/config"

// RemoteConfigurator applies remote machine settings to a backend adapter.
type RemoteConfigurator interface {
	ConfigureRemoteConfig(config.RemoteConfig, string)
}

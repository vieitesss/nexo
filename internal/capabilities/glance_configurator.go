package capabilities

import "github.com/vieitesss/nexo/internal/config"

// GlanceConfigurator applies the shared glance configuration to a backend.
type GlanceConfigurator interface {
	ConfigureGlance(config.GlanceConfig)
}

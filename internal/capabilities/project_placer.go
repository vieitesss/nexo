package capabilities

import (
	"context"
	"os"
	"strings"

	"github.com/vieitesss/nexo/internal/config"
)

// ProjectPlacer opens a project beside the pane the user came from, as a
// split, tab, or floating pane, instead of in its own container.
type ProjectPlacer interface {
	// ProjectBehaviors reports the behaviors this backend implements.
	ProjectBehaviors() []config.ProjectBehavior
	PlaceProject(ctx context.Context, path string, behavior config.ProjectBehavior) error
}

// OriginPaneEnv names the pane a split is placed beside. A launcher whose
// popup is itself a pane, such as a Rex floating block, sets it to the pane
// the user came from; without it each backend uses its own current pane.
const OriginPaneEnv = "NEXO_ORIGIN_PANE"

// OriginPane returns the configured origin pane, or "" when unset.
func OriginPane() string { return strings.TrimSpace(os.Getenv(OriginPaneEnv)) }

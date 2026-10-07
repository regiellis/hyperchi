package hyperchi

import (
	"github.com/regiellis/hyperchi/hyperchi/integrations"
)

// Integrations returns this application's optional capability manager. Route
// groups share their parent's manager. Register before Serve starts the manager.
// With an externally owned HTTP server, call Start and Close explicitly.
// Render through a registered integration with Context.RenderWith.
func (g *HyperChi) Integrations() *integrations.Manager { return g.integrations }

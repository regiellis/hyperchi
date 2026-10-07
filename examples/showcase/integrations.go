package main

import (
	"embed"

	"github.com/CloudyKit/jet/v6"
	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	jetadapter "github.com/regiellis/hyperchi/modules/jet"
	templadapter "github.com/regiellis/hyperchi/modules/templ"
)

//go:generate go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate -f marketing.templ

//go:embed marketing/*
var marketingFiles embed.FS

type studioEstimate struct{ Scope, Price, Timeline, Includes string }

func estimateFor(scope string) (studioEstimate, bool) {
	switch scope {
	case "", "launch":
		return studioEstimate{"Launch website", "$6,000", "4 weeks", "Strategy, five page designs, development, and launch support."}, true
	case "identity":
		return studioEstimate{"Brand identity", "$4,000", "3 weeks", "Positioning, visual identity, and a practical brand field guide."}, true
	case "complete":
		return studioEstimate{"The complete picture", "$9,000", "6 weeks", "A coordinated identity and website, from first workshop to launch day."}, true
	default:
		return studioEstimate{}, false
	}
}

type relayPlan struct{ Name, Price, Description string }
type relayPricing struct {
	Billing, Note string
	Plans         []relayPlan
}

func pricingFor(billing string) (relayPricing, bool) {
	p := relayPricing{Billing: "monthly", Note: "Monthly billing. Cancel before your next renewal.", Plans: []relayPlan{{"Small team", "$18", "A shared home for up to five collaborators."}, {"Growing studio", "$39", "More projects, guest reviews, and team workflows."}}}
	switch billing {
	case "", "monthly":
	case "annual":
		p.Billing = "annual"
		p.Note = "Annual billing: $180 or $390 upfront per year. Prices shown are monthly equivalents."
		p.Plans[0].Price = "$15"
		p.Plans[1].Price = "$32.50"
	default:
		return relayPricing{}, false
	}
	return p, true
}

type trip struct {
	Name        string `json:"name"`
	Terrain     string `json:"terrain"`
	Days        int    `json:"days"`
	Price       int    `json:"price"`
	Description string `json:"description"`
}

var demoTrips = []trip{
	{"The high trail", "mountain", 4, 640, "Ridgelines, early starts, and a mountain hut at the end of the day."},
	{"Along the headlands", "coast", 3, 420, "Salt air, cliff paths, and time to watch the tide roll in."},
	{"Under the canopy", "forest", 2, 280, "A gentle woodland loop with a camp kitchen and a slow morning."},
	{"Above the treeline", "mountain", 6, 960, "A longer traverse for hikers ready to carry a little more."},
}

// setupIntegrations registers the templ and Jet adapters and the three example
// sites. Every route is a HyperChi Handler with an explicit HTML or JSON response.
func setupIntegrations(app *hyperchi.HyperChi) error {
	loader := jet.NewInMemLoader()
	for _, name := range []string{"relay.jet", "pricing.jet"} {
		source, err := marketingFiles.ReadFile("marketing/" + name)
		if err != nil {
			return err
		}
		loader.Set(name, string(source))
	}
	views, err := jetadapter.New(jet.NewSet(loader), "relay.jet", "pricing.jet")
	if err != nil {
		return err
	}
	if err := app.Integrations().Register("jet", views); err != nil {
		return err
	}
	if err := app.Integrations().Register("templ", templadapter.New()); err != nil {
		return err
	}
	app.Get("/examples/templ", func(c *hyperchi.Context) error {
		e, ok := estimateFor(c.Query("scope"))
		if !ok {
			return hyperchi.BadRequest("Unknown project scope")
		}
		return c.RenderWith("templ", studioPage(e))
	})
	app.Get("/examples/templ/estimate", func(c *hyperchi.Context) error {
		e, ok := estimateFor(c.Query("scope"))
		if !ok {
			return hyperchi.BadRequest("Unknown project scope")
		}
		return c.RenderWith("templ", estimateCard(e))
	})
	app.Get("/examples/jet", func(c *hyperchi.Context) error {
		p, ok := pricingFor(c.Query("billing"))
		if !ok {
			return hyperchi.BadRequest("Unknown billing period")
		}
		return c.RenderWith("jet", jetadapter.View{Name: "relay.jet", Data: p})
	})
	app.Get("/examples/jet/pricing", func(c *hyperchi.Context) error {
		p, ok := pricingFor(c.Query("billing"))
		if !ok {
			return hyperchi.BadRequest("Unknown billing period")
		}
		return c.RenderWith("jet", jetadapter.View{Name: "pricing.jet", Data: p})
	})
	app.Get("/examples/alpine", func(c *hyperchi.Context) error {
		return c.View("outpost.tmpl", demoTrips)
	})
	registerTrips(app, "/examples/alpine/trips", demoTrips)
	return nil
}

// registerTrips mounts the trip catalog. It is a JSON endpoint, so the route
// uses hyperchi.JSONErrors: an unknown terrain is a JSON 400 whatever the
// client's Accept header says.
func registerTrips(app *hyperchi.HyperChi, path string, catalog []trip) {
	app.Get(path, tripCatalog(catalog), hyperchi.JSONErrors)
}

// tripCatalog filters the catalog by terrain.
func tripCatalog(catalog []trip) hyperchi.Handler {
	return func(c *hyperchi.Context) error {
		terrain := c.QueryDefault("terrain", "all")
		switch terrain {
		case "all", "coast", "mountain", "forest":
		default:
			return hyperchi.BadRequest("Unknown terrain")
		}
		trips := helpers.FromSlice(catalog).Filter(func(t trip) bool {
			return terrain == "all" || t.Terrain == terrain
		}).OrEmpty()
		return c.JSON(hyperchi.H{"trips": trips, "count": trips.Len()})
	}
}

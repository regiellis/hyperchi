---
title: Passing data
section: Core
order: 50
next: forms
description: Make the request inputs, view model, and response format explicit. The renderer is one step in a data flow you control.
---

## Trace each path {#overview}

- **templ:** scope query → Go fixture selection → component arguments → HTML estimate → htmx swap.
- **Jet:** billing query → Go plan model → Jet execution data → HTML pricing → htmx swap.
- **Alpine:** terrain query → Go trip filter → JSON envelope → fetch → Alpine state → text-bound cards.

The initial marketing pages are HTML in every case. Only the interactive response differs. These examples do not use a database or introduce a data-access framework.

## Validate before building a view model {#example}

Let HyperChi handle routing, request access, collection filtering, and the JSON response. This recipe uses the core `hyperchi` and `helpers` packages, plus `net/http` for status constants. `catalog` is your existing `[]Trip`; keep its public JSON field tags.

```go
app.Get("/trips", func(c *hyperchi.Context) error {
    terrain := c.QueryDefault("terrain", "all")
    switch terrain {
    case "all", "coast", "mountain", "forest":
    default:
        return c.Status(http.StatusBadRequest).
            JSON(hyperchi.H{"error": "Unknown terrain"})
    }

    trips := helpers.FromSlice(catalog).Filter(func(trip Trip) bool {
        return terrain == "all" || trip.Terrain == terrain
    }).OrEmpty()
    return c.JSON(hyperchi.H{"trips": trips, "count": trips.Len()})
})
```

`app.Get` limits this endpoint to GET and propagates returned errors through HyperChi. `c.QueryDefault` handles omitted and empty parameters. `c.Status` sets the status for the `c.JSON` call that follows, which keeps the unknown-terrain error in JSON for every client. `helpers.FromSlice(...).Filter(...)` preserves the element type. `c.JSON` sets the JSON content type and encodes the result; `hyperchi.H` avoids a separate struct for a small response envelope.

The filter helper returns nil when nothing matches, so normalize it to an empty slice if your API promises an array. A `Trip` struct is still useful for typed catalog records; using HyperChi does not require discarding Go types.

The showcase implements this recipe in `tripCatalog`. Unknown terrain returns a JSON error with status 400. Invalid scope or billing values return `hyperchi.BadRequest`, a 400 that HyperChi renders for the client: an alert fragment for htmx, an error page for a browser, and JSON for a client that sends `Accept: application/json`. Omitted values retain the documented defaults.

### Keep service types out of the browser contract

A database record may contain internal identifiers or fields the UI should not receive. Construct a small view model or response struct containing only the required fields. Pass it into templ, expose it through a Jet variable map, or encode it as JSON. Use JSON tags to keep public field names stable if Go names change.

### Add persistence behind the handler

For a real trip catalog, a service method such as `ListTrips(ctx context.Context, terrain string) ([]Trip, error)` can replace the fixture loop. Call it with `c.Ctx()`, handle the error, then render the same response. A SQL implementation should use parameterized queries and `QueryContext`; an HTTP implementation should attach that context to its outgoing request.

Neither the template nor Alpine needs direct database access. Authentication, authorization, and input validation belong before the response model is created. Add a separate write endpoint if the product later needs bookings; the current examples are read-only demonstrations.

### Inspect the wire format

```sh
curl -i 'http://localhost:9999/examples/templ/estimate?scope=launch'
curl -i 'http://localhost:9999/examples/jet/pricing?billing=monthly'
curl -i 'http://localhost:9999/examples/alpine/trips?terrain=forest'
```

The first two return HTML fragments and the third returns JSON. Handlers choose their response explicitly with `c.JSON`, `c.View`, or an external renderer through `c.RenderWith`. The fragment routes are ordinary `app.Get` routes: the handler decides the output, and none of them switches to JSON.

## Read the implementations together {#next}

Compare the three adapters in `examples/showcase/integrations.go`, then browse the [integration gallery guide](/docs/integrations). HyperChi supplies the handler and response APIs. The registered adapter changes between templ and Jet; handlers use the same `c.RenderWith` API. The Alpine page uses HyperChi’s built-in `c.View`.

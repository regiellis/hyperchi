---
title: Alpine.js + JSON
section: Integrations
order: 210
next: helpers
description: Keep the page server-rendered and give one interactive region a JSON contract. Outpost uses Alpine for an outdoor trip finder.
---

## The browser owns the filter state {#overview}

[Open Outpost](/examples/alpine). The marketing page is rendered through `c.View("outpost.tmpl", demoTrips)`. HyperChi exposes this typed slice as `.Result` inside the template. On form submission, the trip finder requests `/examples/alpine/trips?terrain=all`, `coast`, `mountain`, or `forest`. Go returns a filtered collection; Alpine updates the cards.

Alpine is a browser library, not a Go template engine. The server uses `html/template` for the page shell and `encoding/json` for trip data. The example includes a pinned local Alpine runtime, so page interaction does not depend on a CDN request.

## An explicit JSON contract {#example}

```json
{
  "trips": [
    {
      "name": "Coastal weekend",
      "terrain": "coast",
      "days": 3,
      "price": 240,
      "description": "A sample coastal walking trip."
    }
  ],
  "count": 1
}
```

This illustrative response shows the schema; inspect the endpoint for current fixture values. `trips` is an array, `days` and `price` are numbers, and `count` is the number of matching trips. Values are display fixtures rather than availability or checkout quotes.

The initial page includes all four trips as server-rendered cards; it does not fetch on initialization. `static/js/outpost.js` registers the `tripFinder` Alpine component, ignores overlapping submissions, aborts requests after ten seconds, and exposes an error with a retry button. After a successful request, the Alpine-rendered cards replace the initial collection.

### A small Alpine consumer

This minimal consumer shows loading and failure handling. Load Alpine with `defer`, and place the component definition before Alpine initializes:

```html
<script>
function tripFinder() {
  return {
    trips: [], loading: false, error: '',
    async load(terrain = 'all') {
      this.loading = true;
      this.error = '';
      try {
        const response = await fetch(
          '/examples/alpine/trips?terrain=' + encodeURIComponent(terrain)
        );
        if (!response.ok) throw new Error('Unable to load trips');
        const data = await response.json();
        this.trips = data.trips;
      } catch (error) {
        this.error = 'Trips could not load. Please try again.';
      } finally {
        this.loading = false;
      }
    }
  };
}
</script>
<section x-data="tripFinder()" x-init="load()">
  <button @click="load('coast')" :disabled="loading">Coast</button>
  <p x-show="loading" role="status">Loading trips…</p>
  <p x-text="error" role="alert"></p>
  <template x-for="trip in trips" :key="trip.name">
    <article><h3 x-text="trip.name"></h3></article>
  </template>
</section>
```

Disabling the control during loading prevents overlapping requests in this smaller recipe. For controls that remain enabled, cancel earlier fetches or ignore stale responses so a slower old request cannot replace the newest selection.

### Render text as text

Use `x-text` for API strings so they become text content. Avoid `x-html` for untrusted data. Go’s JSON encoder handles JSON syntax; that alone does not make a value safe to insert as HTML. Alpine’s [x-data guide](https://alpinejs.dev/directives/data) explains state scopes, and its [x-text guide](https://alpinejs.dev/directives/text) covers text binding.

Without JavaScript, the marketing content and the full four-trip collection still render. The interactive filtering needs Alpine and the JSON endpoint. The demo makes no booking request and stores no visitor information.

## Use HTML when it is the simpler contract {#next}

If the browser only needs to replace a section, [Jet + htmx](/docs/jet) or [templ + htmx](/docs/templ) can return finished HTML. JSON is useful when the browser needs the individual fields for local state or other JavaScript behavior.

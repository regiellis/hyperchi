---
title: Template filters
section: Reference
order: 230
next: apps
description: Format text, prices, dates, and collections directly in your Go templates. Learn the calling conventions first, then use the reference below to find every registered filter.
---

## Available in every Go template {#overview}

HyperChi copies `filters.FuncMap()` when you create an application. Templates loaded with `app.LoadTemplates` can call these functions immediately. They are Go template functions, not a separate expression language. The external templ and Jet integrations do not inherit this function map.

```go
app := hyperchi.New()
helpers.Must(app.LoadTemplates("templates/*.tmpl"))

app.Get("/product", func(c *hyperchi.Context) error {
    return c.View("product.tmpl", hyperchi.H{
        "name": "  field notebook  ",
        "price": 24.5,
        "published": "2026-10-05T12:00:00Z",
    })
})
```

```html
<h1>{{ .name | trim | title }}</h1>
<p>{{ currency .price "$" }}</p>
<time>{{ .published | dateFormat "Jan 2, 2006" }}</time>
```

The page displays “Field Notebook”, “$24.50”, and “Oct 5, 2026”. The handler uses only the core `hyperchi` package. `c.View` hands the values to the template unchanged, so a `time.Time` works as well as an RFC 3339 string.

## Argument order matters {#arguments}

A Go pipeline places its result in the **last** argument. Unary functions work naturally in a chain. Most multi-argument HyperChi filters take the value **first**, so use a direct call or parentheses.

```html
{{ .name | trim | upper }}           <!-- unary chain -->
{{ truncate .name 20 }}             <!-- value first -->
{{ currency .price "€" }}
{{ default .nickname "Anonymous" }}
{{ join (sort .tags) ", " }}
{{ .published | dateFormat "2006-01-02" }} <!-- format first -->
```

> `{{ .name | truncate 20 }}` is not equivalent to `{{ truncate .name 20 }}`. It sends `20` as the first argument. Likewise, `default` takes the value before the fallback. `dateFormat` intentionally takes the format first and supports the pipeline shown above.

## Recipes with live output {#example}

These outputs are rendered by this page using the installed filters. Change the literals in your own templates to adapt them.

<!-- slot:filter-recipes -->

## Work with collections {#collections}

`sortBy`, `filter`, and `pluck` accept a collection followed by a field name. Use map keys for maps such as `hyperchi.H`. For structs, use the exported Go field name. Sorting uses string representations, including in `sortBy`; sort numeric values in Go when numeric order matters. Keep database queries and business decisions in the handler.

```html
{{ $active := filter .products "active" true }}
{{ range take (sortBy $active "name") 3 }}
  <article>
    <h3>{{ .name }}</h3>
    <p>{{ currency .price }}</p>
  </article>
{{ else }}
  <p>No matching products.</p>
{{ end }}
```

`contains` is a substring test for strings and a membership test for slices and arrays (by the rules of `eq`, so `2` matches `2.0`). `join` expects `[]string`, while many collection transforms return `[]any`. Render those results with `range` instead of passing them to `join`.

## HTML, JSON, and htmx {#html}

Go’s `html/template` escapes ordinary values for their HTML context. Render visitor text directly; adding `escape` can double-escape it. `nl2br` escapes its text before inserting line breaks.

```html
<p>{{ .message }}</p>
<div>{{ nl2br .message }}</div>
<div data-settings="{{ toJSON .settings }}">Settings</div>
<button {{ hxGet "/demo/echo" }} {{ hxTarget "#result" }} {{ hxSwap "innerHTML" }}>
  Load a fragment
</button>
<div id="result" aria-live="polite"></div>
```

Load htmx separately to activate these attributes. Attribute helpers generate individual attributes: place them side by side, not in a pipeline. Keep attribute names, event expressions, and destinations application-controlled.

`safe`, `unescape`, `css`, and `js` mark content as trusted; they do not sanitize it. `stripTags` is a regular-expression transform that also returns trusted HTML, not a security boundary. Only use trusted-content helpers after an appropriate sanitization or application-owned-content boundary.

`htmxButton` and `htmxForm` return complete elements, not attributes. They take typed `filters.HTMXButtonOptions` and `filters.HTMXFormOptions`, not a `dict`. Pass the typed options in the handler data, or use literal elements with individual helpers, as above.

## Register a custom filter {#custom}

Register filters during startup, before loading templates and before serving traffic. Use a distinctive name so you do not accidentally replace a built-in function.

```go
app.RegisterFilter("excerpt", func(limit int, text string) string {
    if limit < 0 {
        limit = 0
    }
    runes := []rune(text)
    if len(runes) > limit {
        return string(runes[:limit]) + "…"
    }
    return text
})
helpers.Must(app.LoadTemplates("templates/*.tmpl"))
```

```html
{{ .description | excerpt 80 }}
```

This custom function deliberately puts text last for pipeline use. Register several functions with `app.RegisterFilters(template.FuncMap{...})`. To use the built-ins outside HyperChi, call `template.New("page").Funcs(filters.FuncMap()).Parse(source)`. Package-level `filters.Register` affects future snapshots; it does not update applications that already copied their function maps.

## Types and failure behavior {#errors}

Functions returning `(value, error)`, such as `fromJSON` and `dateAdd`, stop template execution on a non-nil error. Other helpers return fallback values: an unparseable `date` produces an empty string, while an unconvertible `currency` value is printed unchanged. Validate input in the handler rather than treating formatting as validation.

`default` treats false, numeric zero, and empty collections as empty, as well as empty strings and nil. Use `coalesce` when only nil should trigger a fallback. Validate sizes and indexes before passing visitor-controlled numbers to collection or truncation helpers.

## Complete registered reference {#reference}

Signatures below come from the running filter registry. Arguments appear in call order; `...` means variadic arguments and `interface {}` means `any`. Expand a category to inspect every function. Functions requiring typed options or callbacks are generally easier to use from Go with direct template execution.

<!-- slot:filter-reference -->

## Keep exploring {#next}

Read [Templates](/docs/templates) for composition, [Passing data](/docs/data) for view models, or the [filter implementations and tests](https://github.com/regiellis/hyperchi/tree/main/hyperchi/filters) for detailed edge behavior. Formatting helpers do not automatically transfer to [templ](/docs/templ) or [Jet](/docs/jet); those renderers own their data and helper APIs.

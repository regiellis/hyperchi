# HyperChi template filters

HyperChi registers `filters.FuncMap()` when an application is created. Use the
functions in templates loaded by `app.LoadTemplates`. The documentation site's
`/docs/filters` page includes rendered recipes and the complete registered
reference, with signatures read from the running code.

## Calling conventions

These are ordinary Go template functions. A pipeline passes its result as the
**last** argument. Most multi-argument filters expect the value **first**: call
them directly, or use parentheses to compose operations.

```gotemplate
{{ .Name | trim | title }}
{{ truncate .Title 50 }}
{{ truncateWords .Description 10 }}
{{ replace .Text "old" "new" 1 }}
{{ currency .Price "$" }}
{{ percent .Rate 1 }}
{{ default .Nickname "Anonymous" }}
{{ date .CreatedAt "2006-01-02" }}
{{ .CreatedAt | dateFormat "Jan 2, 2006" }}
```

Do not write `.Title | truncate 50` or `.Price | currency "$"`: those pass
arguments in the wrong order. `dateFormat` explicitly takes the format first.

## Strings and collections

```gotemplate
{{ .Title | trim | upper }}
{{ slug .Title }}
{{ pluralize "item" (len .Items) }}
{{ join (split "go,html,htmx" ",") " / " }}
{{ range take (sortBy .Products "name") 3 }}
  <h2>{{ .name }}</h2>
  <p>{{ currency .price }}</p>
{{ end }}
```

Use JSON map keys for page data and exported field names when passing structs
directly. `contains` takes the container first: `{{ contains .Title "go" }}`
is a substring test on a string, and `{{ contains .Tags "go" }}` is membership
in a slice or array, compared by the rules of `eq`. `join` requires
`[]string`; transforms such as `sort`, `unique`, and `pluck` return `[]any`.
Use `range` to render those results.

## HTML and htmx

`html/template` automatically escapes ordinary text. Do not add `escape` to
visitor values by default; that can double-escape them. `nl2br` escapes text
before inserting line breaks.

```gotemplate
<p>{{ .Message }}</p>
<div>{{ nl2br .Message }}</div>
<button {{ hxGet "/hello" }} {{ hxTarget "#result" }} {{ hxSwap "innerHTML" }}>
  Load
</button>
<div id="result" aria-live="polite"></div>
```

Load htmx separately. Put independent attribute helpers beside each other;
do not chain them with a pipeline. Keep event expressions application-controlled.
Every filter that turns its input into an attribute name checks it: `attr` and
`jsonAttr` (the name), `hxOn` (the event), `hxStatus` (the pattern), the map
keys of `dataAttrs`, `ariaAttrs`, and `htmxLink`, the `On` keys of `htmxAttrs`,
and the `Attrs` keys of `inputAttrs`, `htmxButton`, and `htmxForm`. A name must
start with a letter or `_` and hold only letters, digits, and `_ . : -`. Any
other name stops the render with an error. Map keys are written in sorted order. `hxVals`,
`hxHeaders`, and `hxConfig` write JSON, HTML-escaped inside a double-quoted
attribute, so any value is safe there.

`htmxButton` and `htmxForm` produce complete elements. Their arguments are
`filters.HTMXButtonOptions` and `filters.HTMXFormOptions`, not dictionaries. Use
typed options with direct Go template execution; for JSON-based page data,
prefer literal HTML with individual attribute helpers.

`safe`, `unescape`, `css`, and `js` bypass context escaping; they do not sanitize
content. `stripTags` returns trusted HTML after a regular-expression transform
and is not a sanitizer. Do not use these functions as a trust boundary for
visitor input.

## JSON, dates, and numbers

```gotemplate
<div data-settings="{{ toJSON .Settings }}">Settings</div>
{{ jsonGet (fromJSON `{"user":{"name":"Morgan"}}`) "user.name" }}
{{ dateAdd .CreatedAt 7 "days" | dateFormat "2006-01-02" }}
{{ date (timezone .CreatedAt "America/New_York") "Jan 2, 2006 15:04" }}
{{ number .Price 2 }}
{{ bytes .FileSize }}
{{ humanize .Views }}
```

Use RFC 3339 date strings in the JSON page-data pipeline. `percent` multiplies
its value by 100: `percent 0.125 1` produces `12.5%`.

Functions returning `(value, error)` stop template execution on a non-nil error.
Other helpers have fallback behavior: `date` returns an empty string for invalid
time input; `currency` prints an unconvertible value unchanged. Validate data in
handlers. Validate visitor-controlled sizes and indexes before using them in
truncation or collection helpers.

`default` treats zero, false, empty strings/collections, and nil as empty.
`coalesce` selects the first non-nil argument instead.

## Custom filters

Register application filters before loading templates and before serving traffic:

```go
app.RegisterFilter("shout", func(s string) string {
    return strings.ToUpper(s) + "!"
})
app.RegisterFilters(template.FuncMap{
    "double": func(n int) int { return n * 2 },
})
helpers.Must(app.LoadTemplates("templates/*.tmpl"))
```

Imports for the custom functions above are `strings` and `html/template`.
For a pipeline-friendly multi-argument function, put the piped value last.

Outside HyperChi, install the map before parsing:

```go
tmpl, err := template.New("page").Funcs(filters.FuncMap()).Parse(source)
```

Package-level `filters.Register` and `filters.RegisterMap` change future
`FuncMap()` snapshots, not applications that already copied a map. External
templ and Jet renderers do not automatically receive HyperChi filters.

## Source reference

- [Strings](string.go): casing, truncation, slugs, splitting and joining.
- [HTML](html.go): trusted markup, attributes, classes, and form elements.
- [JSON](json.go): encoding, decoding, paths, and JSON attributes.
- [Dates](date.go): formatting, arithmetic, timezones, and durations.
- [Numbers](number.go): currency, percentages, arithmetic, and display formats.
- [Collections](collection.go): selection, sorting, grouping, and transforms.
- [HTMX](htmx.go): request attributes and typed element builders.
- [Utilities](utility.go): defaults, comparisons, dictionaries, lists, and encoding.

Run `go test ./hyperchi/filters` from the repository root. The showcase's
`TestFilterDocumentation` also verifies the published reference covers the
registry and executes its rendered examples.

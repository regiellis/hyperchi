# HyperChi helpers

Import `github.com/regiellis/hyperchi/hyperchi/helpers` for typed collections, JSON objects, outbound HTTP, strings, files, logging, and optional values. The [helpers guide](../../examples/showcase/templates/helpers.tmpl) covers request handlers and response helpers too.

## Request handlers

Every route is a `func(c *hyperchi.Context) error` registered with `app.Get`, `Post`, `Put`, `Patch`, or `Delete`. Read inputs with `c.Param`, `c.ParamInt`, `c.Query`, `c.QueryDefault`, `c.QueryInt`, and `c.Queries`. Query defaults apply to absent and empty values. `hyperchi.Bind[T](c)` decodes and validates a whole form or JSON body.

`c.JSON` sets the content type and serializes before writing. For a specific status, chain it: `c.Status(http.StatusAccepted).JSON(data)`. Return errors instead of writing them: `hyperchi.BadRequest(msg)` and the other `HTTPError` constructors pick the status, and the app's error handler renders it for the client. Network write errors may occur after the response is committed.

`c.View`, `c.Page`, and `c.Fragment` accept `hyperchi.H`, `helpers.JSON`, and `map[string]any` as top-level template keys. Structs and slices appear under `.Result`. `c.RenderWith` renders through a registered integration; see the integration manager guide.

## Typed collections

```go
trips := helpers.FromSlice(catalog).Filter(func(t Trip) bool {
    return t.Terrain == "coast"
}).OrEmpty()
first, found := trips.First()
count := trips.Len()
```

`Filter` preserves the element type and allocates its result. `OrEmpty` turns a nil slice into an empty slice so JSON emits `[]`; non-nil slices are returned without copying. `First`, `Last`, and `Get` return `(T, bool)`. `GetOr` supplies a fallback. `FromSlice` shares its backing array with the source.

Use package functions `Map`, `Reduce`, `GroupBy`, `ToMap`, and `FlatMap` for operations involving additional type parameters. `app.Slice` uses `any`; use the generic package for typed models. See [collections.go](collections.go) for sorting, partitioning, sets, indexing, and aggregation.

## JSON objects

```go
options := helpers.NewJSON().Set("limit", "24").Set("enabled", false)
limit := options.GetIntOr("limit", 12)
enabled := options.GetBoolOr("enabled", true)
```

`GetIntOr` and `GetFloatOr` use the fallback for missing or invalid values and preserve valid zero values. Integer access accepts every Go integer type, in-range `float32`/`float64` (truncated), `json.Number`, and integer strings. Float access accepts the same numeric types. Boolean access accepts bool values and preserves false. `GetJSON` accepts plain maps or nested `helpers.JSON`.

`FromJSON` parses an object; JSON `null` becomes an empty object. Arrays and scalar values produce errors. Use `ParseJSON[T]` for other shapes. `FromJSONOr` explicitly discards parsing errors. `Set` and `Merge` allocate when necessary: retain their returned map. `Clone` is shallow, and maps are not safe for concurrent writes.

`ToJSON`, `ToString`, and their pretty variants return `{}` on encoding errors. Use `json.Marshal` or `c.JSON` when the error matters. Prefer typed structs for stable API contracts and JSON objects for small dynamic envelopes.

## Outbound HTTP

Configure one client before serving requests:

```go
client := helpers.NewHTTPClient().
    WithBaseURL("https://api.example.com").
    WithTimeout(5 * time.Second).
    WithJSONHeaders()
```

Use the incoming request context in a handler:

```go
res, err := client.GetWithContext(c.Ctx(), "/items")
if err != nil {
    return err
}
defer res.Close()
if !res.IsSuccess() {
    return hyperchi.Errorf(http.StatusBadGateway, "inventory service returned %d", res.StatusCode())
}
var items []Item
if err := res.Parse(&items); err != nil {
    return err
}
return c.JSON(helpers.FromSlice(items).OrEmpty())
```

This snippet assumes your `Item` model, a context handler, and imports for `fmt` and `time`.

All six methods have context variants: `GetWithContext`, `PostWithContext`, `PutWithContext`, `PatchWithContext`, `DeleteWithContext`, and `PostFormWithContext`. Shorter methods use a background context; the default client timeout is 30 seconds. Configuration methods mutate the client and must finish before concurrent requests.

Strings are sent raw; other non-nil request bodies are JSON encoded. Configure JSON headers explicitly. `PostFormWithContext` sets form content type without mutating shared client headers. HTTP error statuses are responses, not transport errors. Always check `err` before using a response and check status separately. `Parse`, `Text`, and `JSON` consume and close the body, and report a failed close as an error. Defer `Close` to cover early returns; choose one body reader per response.

## Error handling and optional values

Return errors from request handlers. Use `Must` and `MustValue` for startup requirements. Use an explicit fallback only when failure is acceptable:

```go
value, err := strconv.Atoi(configuredPort)
port := helpers.Default(value, err, 8080)
```

`Default` and `OrLogDefault` take three arguments; a multi-return function call cannot be mixed with a fallback argument. `Ignore` and `IgnoreValue` deliberately discard errors.

`helpers.Ptr(value)`, `Value(ptr)`, and `ValueOr(ptr, fallback)` provide typed pointer helpers. `NewSafeSlice(slice).First()` returns a `Safe[T]` wrapper; this differs from `Slice[T].First()`, which returns `(T, bool)`.

## Strings and files

`Str` counts runes everywhere: `Len`, `Index`, `LastIndex`, `Slice`, `Left`, `Right`, `Center`,
and the `Pad` methods agree, so `s.Left(s.Index(":"))` never splits a character. Padding repeats
the first rune of the fill argument. `ToInt`, `ToFloat`, and `Matches` return errors. `ToIntOr` and
`ToFloatOr` are the fallback spellings.

The case converters split words at separators and case changes (`userID` gives `userId` and
`user_id`). `Slugify` splits only at separators (`"iPhone 15"` gives `iphone-15`). All of them
transliterate accented Latin letters (`é` to `e`, `ß` to `ss`, `ø` to `o`) and drop letters with no
ASCII spelling.

`ReadFile` and `ReadFileLines` return the read error. `ReadFileOr` is the explicit fallback.
`Env.Save` and `SwitchEnvironment` write `.env` files with mode `0600`. In a `.env` file, a `#`
after whitespace starts a comment (`KEY=value # note`); a `#` inside quotes or with no space before
it (`pa#ss`) is part of the value.

## Profiling and logging

Each `Profiler.StartTimer` call returns its own run. Keep it and call `Stop`, which is reliable
when runs of one name overlap. `StopTimer(name)` stops the newest running run of that name.
`StatsHandler()` serves `GetStats` as JSON. Mount it on a development-only or authenticated route.

`Logger`, the package-level logger setters, and `SetDefaultLogger`/`SetDefaultSilent` are safe to
call while other goroutines log.

## More APIs

- [strings.go](strings.go): fluent string transformations (`helpers.S(value).Trim().Lower().String()`).
- [collections.go](collections.go): typed slice operations.
- [json_http.go](json_http.go): JSON, HTTP clients, and query parameters.
- [errors.go](errors.go): results, fallback policies, retries, and error wrapping.
- [pointers.go](pointers.go): pointers and optional values.

Application helpers also expose logging (`app.LogInfo`, `LogWarn`, `LogError`), configuration (`app.EnvOr`), and files. Keep authorization, domain validation, and error policy explicit rather than hiding them in convenience calls.

## Compatibility notes

Direct template rendering now treats `hyperchi.H` and `helpers.JSON` like plain maps. Change `.Result.Title` to `.Title` for those map inputs, or explicitly pass `hyperchi.H{"Result": data}` to retain a wrapper. Struct and slice rendering is unchanged. Existing HTTP methods remain available. `Filter` retains its nil-empty behavior; `OrEmpty` is opt-in. JSON numeric fallbacks now apply to invalid present values as well as missing keys.

v0.2.0 changes signatures and offsets. `ReadFile` returns `(string, error)` and `ReadFileLines` returns `([]string, error)`. `Str.ToInt`, `ToFloat`, and `Matches` return an error as their second value. `Str` offsets and widths count runes instead of bytes. `Profiler.EnableProfileEndpoint` is replaced by `StatsHandler`, and `Timer` no longer carries `Count` or `TotalTime` (read them from `GetStats().CustomTimers`). The case converters split acronyms (`HTTPServer` gives `http_server`), and `Slugify` lowercases before splitting.

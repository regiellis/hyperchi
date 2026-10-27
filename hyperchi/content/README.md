# content

Package `content` loads a directory of Markdown, MDX, HTML, and text files into
collections you can validate and query from Go. It has no database and no HTTP
surface: your handlers call it and render the results with your own templates.

```go
import "github.com/regiellis/hyperchi/hyperchi/content"
```

In a HyperChi app, `app.Content.LoadDir("content")` runs this loader and puts
the collections in `app.Content`, where `Get`, `Query`, and `ContentRoutes`
serve them; in development it also reloads changed files. Use the package
directly when you want its queries (search, pattern filters, numeric sorts) or
no app at all.

The examples below mirror `example_test.go`, which runs them against
`testdata/site`.

## Layout

Each immediate subdirectory of the base directory is a collection. Every file
with a supported extension anywhere under it becomes one `Content`. Files
directly under the base directory belong to no collection and are skipped.

```
content/
├── blog/
│   ├── hello.md          → ID "blog-hello", slug "hello"
│   └── 2024/recap.md     → ID "blog-2024-recap", slug "recap"
└── pages/
    └── about.html        → ID "pages-about", slug "about"
```

| Extension        | `Content.HTML`                                                        |
| ---------------- | --------------------------------------------------------------------- |
| `.md`, `.mdx`    | A small Markdown subset: headings, paragraphs, `-` lists, code fences, or the output of `ContentConfig.Markdown` |
| `.html`, `.htm`  | The body as written (treat content files as trusted input)            |
| `.txt`           | The escaped body inside `<pre>`                                       |

MDX is rendered as Markdown. Components embedded in MDX are not evaluated.

A collection's `Type` is the type most of its files have. A tie goes to
Markdown, then MDX, HTML, and text, so the result never depends on which file
the loader reads first. A collection declared with a `Type` keeps it.

## Loading

```go
layer := content.NewContentLayer(&content.ContentConfig{BaseDir: "testdata/site"})
layer.DefineCollection(content.CollectionConfig{Name: "blog", Schema: content.BlogPostSchema()})
if err := layer.LoadContent(); err != nil {
	log.Fatal(err)
}

blog, _ := layer.GetCollection("blog")
page, err := blog.Query(content.ContentQuery{
	Where: map[string]any{"published": true},
	Sort:  []content.SortCriteria{{Field: "publishedAt", Order: "desc"}},
})
if err != nil {
	log.Fatal(err)
}
for _, post := range page.Contents {
	fmt.Println(post.GetTime("publishedAt").Format("2006-01-02"), post.GetString("title"), post.GetStringSlice("tags"))
}
// 2024-04-10 Second Post [go]
// 2024-03-01 Hello, World [go htmx]
```

`LoadContent` reads every file, then installs the collections:

- An undeclared collection is installed as loaded, with no schema.
- A collection declared with `DefineCollection` is rebuilt from its
  declaration, so every loaded file passes through its schema. Files that fail
  are left out and reported in the returned error (one joined error per
  file: a `*ContentError`, or a schema validator's own error prefixed with
  the file's path). The valid files are still installed.
- A collection that an earlier load found on disk and this one did not is
  removed, or emptied if it was declared.

A missing base directory loads nothing and is not an error. A file that cannot
be read stays in its collection with the read error under `Data["_error"]`.

Each load replaces collections instead of changing them, so a `*Collection` you
hold keeps the state it had. Call `GetCollection` again for the current one.
`GetAllCollections` returns a copy of the name-to-collection map.

### Embedded content

Set `ContentConfig.FS` to read from an `fs.FS`, such as a `go:embed`
directory, instead of the disk. `BaseDir` is then a slash-separated directory
inside it (`""` or `"."` for its root), and `Content.Path` holds paths inside
the file system:

```go
//go:embed content
var site embed.FS

layer := content.NewContentLayer(&content.ContentConfig{FS: site, BaseDir: "content"})
```

IDs, slugs, front matter, and HTML come out the same as from the disk. An
`embed.FS` records no modification times, so `CreatedAt` and `UpdatedAt` are
zero; keep dates in front matter. A layer reading an `FS` never watches, even
with `WatchForChanges` set.

### Markdown renderer

The built-in Markdown subset has no links or inline formatting. Set
`ContentConfig.Markdown` to render `.md` and `.mdx` bodies (front matter
already removed) with a full renderer, such as `modules/markdown`:

```go
md := markdown.New()
layer := content.NewContentLayer(&content.ContentConfig{
	BaseDir: "content",
	Markdown: func(body string) (template.HTML, error) {
		doc, err := md.Parse([]byte(body))
		if err != nil {
			return "", err
		}
		return doc.HTML, nil
	},
})
```

Its output is trusted as HTML. A file it fails on stays in its collection with
the error under `Data["_error"]`, like a file that cannot be read.

## Front matter

A file may start with a header between two `---` lines, one `key: value` per
line. Lines starting with `#` are comments. A line that is not `key: value`
(such as a nested YAML list) or a key given twice fails the file, and the
load error names the line. Values are typed like this:

| Written                      | Stored as                         |
| ---------------------------- | --------------------------------- |
| `true`, `false`              | `bool`                            |
| `3`                          | `int`                             |
| `4.5`                        | `float64`                         |
| `[go, "htmx"]`               | `[]string`                        |
| `"02134"`, `'1.0'`           | `string` (quotes keep it text)    |
| anything else                | `string`                          |

Front matter has no date type, so dates load as strings. A schema field of type
`FieldTypeTime` converts a date string, quoted or not, to `time.Time` during
validation. The accepted forms are the ones `ParseTime` reads:

- RFC 3339: `2024-01-15T10:00:00Z`, `2024-01-15T12:00:00+02:00`
- a date: `2024-01-15`
- a date and time: `2024-01-15 10:30`, `2024-01-15T10:30`, with optional seconds

Values without a zone are UTC. Forms such as `01/02/2024` are rejected because
the day and month order is ambiguous. Fields that are not time-typed keep the
string. `Content.GetTime` also parses these forms, so it works in collections
without a schema.

## Schemas

Build a schema with `NewSchema`. `Field` starts a field, the methods after it
configure that field, and `Build` closes the field list:

```go
schema := content.NewSchema("Recipe").
	Field("title", content.FieldTypeString).Required().MinLength(3).
	Field("servings", content.FieldTypeInt).Min(1).Default(2).
	Field("cookedOn", content.FieldTypeTime).
	Build().
	Build()

ok := &content.Content{Data: map[string]any{"title": "Soup", "cookedOn": "2024-01-15"}}
if err := ok.ValidateSchema(schema); err != nil {
	log.Fatal(err)
}
fmt.Println(ok.GetInt("servings"), ok.GetTime("cookedOn").Weekday())
// 2 Monday

bad := &content.Content{Path: "recipes/x.md", Data: map[string]any{"title": "Ox"}}
fmt.Println(bad.ValidateSchema(schema))
// validation error in recipes/x.md: constraint violation for field title: constraint error: value too short
```

| Field method                       | Effect                                                     |
| ---------------------------------- | ---------------------------------------------------------- |
| `Required()`                       | The field must be present                                  |
| `Default(v)`                       | Value set when the field is missing                        |
| `MinLength(n)`, `MaxLength(n)`     | Length in characters for strings, elements for slices      |
| `Min(v)`, `Max(v)`                 | Numeric bounds, comparing any mix of int and float         |
| `Validate(func(any) error)`        | Custom check on the field's value                          |
| `Description(s)`                   | Documentation only                                         |

Field types are `FieldTypeString`, `FieldTypeInt`, `FieldTypeFloat` (an int is
accepted), `FieldTypeBool`, `FieldTypeTime`, `FieldTypeSlice`, `FieldTypeMap`,
and `FieldTypeAny`. On the schema, `Required(names...)` lists required fields,
`Validate(func(*Content) error)` adds a whole-document check whose error is
returned as is, and `Transform(func(*Content) *Content)` rewrites each document
after it validates. `Collection.AddContent` validates, then transforms.

`examples.go` has ready-made schemas to copy or use directly: `BlogPostSchema`,
`ProductSchema`, `DocumentationSchema`, `ProjectSchema`, `EventSchema`, and
`RecipeSchema`.

## Typed models, relations, and generation

Collections v2 extends this API with:

- `SchemaFor[T](name, validators...)`: derive a schema from JSON field names and
  `content:"required,ref=authors"` struct tags.
- `DecodeData[T](data)` and `Decode[T](document)`: strict, independently owned
  typed front matter, with document metadata in the latter case.
- `QueryTyped[T](collection, query)`: typed entries and the pre-pagination total.
- `References(collection)` and `Elements(type)` on field builders.
- `ValidateRelations(snapshot)` and `ResolveReferences(snapshot, item, field)`:
  exact content-ID references, including forward references and cycles.
- `GenerateTypes(packageName, schemas)`: deterministic, formatted Go source.

See the [typed collections guide](../../examples/showcase/docs/content-v2.md)
for complete examples, supported model types, and a `go generate` workflow.
`example_v2_test.go` contains runnable schema, query, relation, and generation
examples.

Relations are checked after all collection schemas run. A broken reference
rejects the candidate load and retains the previous snapshot. Ordinary schema
errors still follow the partial-load behavior documented above. Use one
`GetAllCollections` snapshot for related lookups. Path-normalization collisions
in content IDs now fail a load rather than overwrite an unrelated document.

## Queries

```go
page, err := notes.Query(content.ContentQuery{
	Where: map[string]any{"title": "go*"}, // prefix match, case-insensitive
	Sort:  []content.SortCriteria{{Field: "order", Order: "desc"}},
	Limit: 1,
})
```

`Collection.Query` applies, in order:

1. `Where`: every entry must match. A value matches by equality. A string with
   a `*` at the start, end, or both matches as a case-insensitive suffix,
   prefix, or substring. A `[]string` (or `[]any`) matches a field of the same
   slice type when the two share an element.
2. `Search`: a case-insensitive substring of the body, a string field, or an
   element of a `[]string` field.
3. `Sort`: criteria in priority order, `Order` `"asc"` (default) or `"desc"`.
   `id`, `slug`, `path`, `createdAt`, and `updatedAt` read the system fields.
   Numbers compare as numbers across int and float, times as times. Without a
   sort, results are ordered by ID, so pages are stable.
4. `Offset` and `Limit` (`0` means all). An offset past the end gives an empty
   page.
5. `Select`: keep only the named data fields (`body` and `html` copy those
   system fields).

The result carries `Total`, `Page`, `PageSize`, `TotalPages`, `HasNext`, and
`HasPrev`. `Collection.GetStats` counts distinct values of the schema's string
and slice fields.

Read fields with `Get`, `GetString`, `GetInt`, `GetBool`, `GetTime`, `GetSlice`
(any slice as `[]any`), and `GetStringSlice`. Each returns the zero value when
the field is missing or has another type.

## Watching for changes

```go
layer := content.NewContentLayer(&content.ContentConfig{
	BaseDir:         "content",
	WatchForChanges: true,
	WatchInterval:   time.Second,
	OnReload: func(changed []string, err error) {
		if err != nil {
			log.Printf("content reload: %v", err)
			return
		}
		log.Printf("reloaded after %d changed files", len(changed))
	},
})
if err := layer.LoadContent(); err != nil {
	log.Fatal(err)
}
defer func() {
	if err := layer.Close(); err != nil {
		log.Print(err)
	}
}()
```

With `WatchForChanges` set, the first `LoadContent` starts one goroutine that
polls `BaseDir` every `WatchInterval` (two seconds when zero). When a file with
a content extension is created, modified, or removed, it reloads the whole
layer and calls `OnReload` with the changed paths and any error. Without
`OnReload`, errors are logged through `slog.Default()`. `OnReload` runs on the
watcher goroutine and must not call `Close`. `Close` stops the goroutine and
waits for it. A later `LoadContent` starts a new one.

`DefaultContentConfig` turns `WatchForChanges` on. Turn it off in production,
or call `Close` when the server stops.

The polling itself is available as `FileWatcher`: `NewFileWatcher(dir)`, then
`Scan` for one comparison against the previous scan, or `Watch(ctx, interval,
onChange)` to poll until the context ends. Scan errors (an unreadable
directory, for example) are returned, never dropped.

## Concurrency

`ContentLayer` methods are safe to call from many goroutines while the watcher
reloads. A `Collection` is not synchronized: `AddContent` and `RemoveContent`
on a collection that handlers are reading need your own lock. Collections
installed by a load are never changed by a later load.

## Not implemented

- No template functions, HTTP endpoints, or full-text index.
- No caching layer and no component rendering: MDX components are left as
  written (see above).

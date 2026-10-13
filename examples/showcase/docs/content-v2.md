---
title: Typed content collections
section: Built-ins
order: 102
next: publishing
description: Decode front matter into Go structs, validate references between collections, and generate model types from schemas.
---

## Add types to existing collections {#overview}

Content collections v2 extends `hyperchi/content` with typed models, references, and deterministic Go type generation. The existing loader, schema builder, query API, file formats, and watchers continue to work. You can adopt typed models one collection at a time.

`SchemaFor[T]` derives a schema from a Go struct. `DecodeData[T]` decodes front matter into that struct, and `Decode[T]` also copies a document's ID, slug, path, body, HTML, and timestamps. `QueryTyped[T]` uses the existing collection query and decodes its results.

## Define and load a model {#example}

```go
type Post struct {
    Title     string    `json:"title" content:"required"`
    Published time.Time `json:"published"`
    Author    string    `json:"author" content:"required,ref=authors"`
    Related   []string  `json:"related" content:"ref=posts"`
    Tags      []string  `json:"tags"`
    Draft     bool      `json:"draft"`
}

posts, err := content.SchemaFor[Post]("Post", func(post Post) error {
    if strings.TrimSpace(post.Title) == "" {
        return errors.New("title must not be blank")
    }
    return nil
})
if err != nil {
    return err
}
layer := content.NewContentLayer(&content.ContentConfig{BaseDir: "content"})
layer.DefineCollection(content.CollectionConfig{Name: "posts", Schema: posts})
if err := layer.LoadContent(); err != nil {
    return err
}
```

For that model, `content/posts/hello.md` can contain:

```markdown
---
title: Hello
author: authors-regi
related: []
tags: [go, htmx]
published: 2026-10-08
draft: false
---

Hello from a typed collection.
```

The referenced author lives at `content/authors/regi.md`, whose content ID is `authors-regi`. Each collection may have its own typed or builder-defined schema; undeclared collections still load normally.

JSON tags supply exact, case-sensitive front matter names. Without a JSON tag, the Go field name is used. `content:"required"` requires presence, not a nonzero value: `false`, `0`, and an empty string are present. Use a typed validator for stronger rules. `json:"-"` and unexported fields are ignored.

Supported model fields are strings, signed integers, floats, booleans, `time.Time`, slices of strings/numbers/bools/any, `map[string]any`, `any`, and optional pointers to scalar values or `time.Time`. Missing optional fields keep their zero value. Explicit nulls are rejected. Embedded fields, nested structs, arrays, unsigned integers, nested slices, custom map element types, and slices of dates are rejected during schema construction.

Unknown front matter fields, invalid scalar conversions, integer overflow, and malformed dates produce errors. Dates use the existing unambiguous `content.ParseTime` formats. Decoding uses JSON as the conversion boundary and does not share slices or maps with its input. Numbers decoded into `any` or `map[string]any` remain `json.Number` to preserve precision.

## Query and resolve references {#relations}

Capture one snapshot for a query and its relation lookups:

```go
snapshot := layer.GetAllCollections()
entries, total, err := content.QueryTyped[Post](snapshot["posts"], content.ContentQuery{
    Where: map[string]any{"draft": false},
    Sort: []content.SortCriteria{{Field: "published", Order: "desc"}},
    Limit: 10,
})
if err != nil {
    return err
}
for _, entry := range entries {
    source := snapshot["posts"].Contents[entry.ID]
    authors, err := content.ResolveReferences(snapshot, source, "author")
    if err != nil {
        return err
    }
    fmt.Println(entry.Data.Title, authors[0].GetString("name"), total)
}
```

`total` counts matches before pagination. A decoding failure returns an error and no partial typed result. `Select` still applies before decoding, so selecting away a required field will fail. Typed query results own their data; raw relation results belong to the snapshot and must be treated as read-only.

References contain exact content IDs, not slugs. String fields name one target; string slices name multiple targets in their declared order. An absent optional reference returns an empty slice. Empty IDs, duplicate IDs in a reference list, missing collections, missing documents, and targets that failed to load produce errors. Forward references and cycles are valid because checking runs after all collection schemas have been applied. Resolution does not recursively expand the graph.

Builder schemas can declare the same relationships:

```go
schema := content.NewSchema("Post").
    Field("author", content.FieldTypeString).Required().References("authors").
    Field("related", content.FieldTypeSlice).References("posts").
    Field("tags", content.FieldTypeSlice).Elements(content.FieldTypeString).
    Build().Build()
```

`Elements` validates each slice value and supplies its generated Go element type. Unspecified slice types remain unconstrained, except reference slices, which must contain string IDs. Use `content.ValidateRelations(snapshot)` after manually building collections; standalone `AddContent` cannot validate targets in other collections.

A reload with broken references rejects the entire candidate snapshot and preserves the previous collections. Readers can continue using their old snapshot. Ordinary per-file schema failures retain the existing partial-load behavior: invalid files are omitted and reported. A reference to an omitted file causes the reference check to reject that candidate. Files whose paths normalize to the same ID now fail the load instead of silently replacing each other.

All relation targets must be in the same `ContentLayer` snapshot. Keep schema declarations and raw collection data immutable while handlers read them. Validation callbacks should check their input rather than query the layer being loaded.

A rejected explicit `app.Content.LoadDir` or `LoadFS` call preserves previously installed data and watchers. It does not start a new watcher for the rejected source; fix the source and retry the load. An existing watcher continues watching after a rejected reload.

## Use app.Content {#app-content}

The same typed schema works with the application loader:

```go
if err := app.Content.LoadDir("content",
    hyperchi.WithContentCollection(content.CollectionConfig{
        Name: "posts", Schema: posts,
    }),
); err != nil {
    return err
}
item, err := app.Content.Get("posts", "hello")
if err != nil {
    return err
}
post, err := content.DecodeData[Post](item.Metadata)
if err != nil {
    return err
}
fmt.Println(post.Title)
```

The application loader validates relations within each loaded source. Its public lookup API still uses slugs, and manually created items retain their existing validation behavior. For ID-based relation resolution and typed collection queries, use `ContentLayer` directly as above. No existing handler API needs to change.

## Generate model types {#generate}

If builder schemas are your source of truth, `GenerateTypes` emits structs with JSON names, required tags, and reference tags. Pass exported Go type names as map keys:

```go
source, err := content.GenerateTypes("models", map[string]*content.ContentSchema{
    "Post": schema,
})
if err != nil {
    return err
}
if err := os.WriteFile("content_gen.go", source, 0644); err != nil {
    return err
}
```

The function returns formatted Go source and performs no filesystem writes. Type and field ordering is deterministic. Invalid identifiers, field-name collisions, unknown field types, and invalid reference declarations fail before any source is returned. Dates add a `time` import. Generated slices use `ElementType`, reference slices use `[]string`, and unconstrained slices use `[]any`.

Generated fields use canonical wire types: `int`, `float64`, `string`, `bool`, `time.Time`, slices, maps, or `any`. They do not reproduce named Go aliases or optional pointers from a type-derived schema. Defaults, constraints, custom validators, and transformations remain in the original runtime schema; generating a struct does not encode those rules into Go's type system.

For `go generate`, put a small generator in `internal/gencontent/main.go`, import your schema definitions, and call `GenerateTypes` followed by `os.WriteFile`. In the destination package add:

```go
//go:generate go run ../internal/gencontent
```

Adjust that relative path for your project. Go runs the generator from the package directory; create any output directories explicitly. Check generated files into source control and rerun generation after schema changes. Hand-written models with `SchemaFor` do not need generation.

## Keep going {#next}

The [publishing guide](/docs/publishing) turns selected content into sitemaps and feeds. The [Markdown content guide](/docs/content) covers front matter, rendering, and watching files.

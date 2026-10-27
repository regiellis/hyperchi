# HyperChi Database Integration

A lightweight SQLite wrapper with PocketBase-inspired optimizations, designed for the HyperChi microframework.
It runs on [modernc.org/sqlite](https://gitlab.com/cznic/sqlite), a pure-Go driver, so it builds with
`CGO_ENABLED=0` and cross-compiles without a C toolchain.

## Features

- **Zero Configuration**: Works out of the box with sensible defaults
- **PocketBase Optimizations**: WAL mode, connection pooling, optimized PRAGMA settings  
- **Type-Safe Queries**: Generic functions for structured data
- **Simple ORM**: Essential CRUD operations without complexity
- **Migration Helpers**: Table builder and common migrations
- **Query Builder**: Fluent API for complex queries
- **Validation**: Built-in input validation helpers
- **Pagination**: Easy pagination with metadata
- **Utilities**: Slug generation, backup tools, and more

## Quick Start

```go
package main

import "github.com/regiellis/hyperchi/hyperchi"

func main() {
    // Database is automatically initialized
    app := hyperchi.New()
    
    // Run migrations
    app.Migrate(
        database.CommonMigrations{}.Users(),
        database.CommonMigrations{}.Posts(),
    )
    
    // Create a user
    userID, err := app.DB.Create("users", map[string]interface{}{
        "email": "user@example.com",
        "name":  "John Doe",
    })
    
    // Query users
    users, err := app.DB.List("SELECT * FROM users WHERE active = 1")
    
    app.Run()
}
```

## Configuration

```go
// Custom database configuration
config := &database.DBConfig{
    Path:         "./myapp.db",
    MaxOpenConns: 25,
    MaxIdleConns: 10,
    Debug:        true,
}

app.InitDB(config)
```

`Path` may carry a DSN query in the driver's syntax, for example
`./myapp.db?_pragma=cache_size(-2000)&_txlock=immediate`. NewDB adds its defaults (WAL,
foreign keys, `synchronous=NORMAL`, a 64MB cache, incremental auto-vacuum, memory temp store,
256MB mmap) where the DSN does not set that PRAGMA itself, and `Timeout` always sets
`busy_timeout`. Opening fails if the DSN turns WAL or foreign keys off. Times are written in
SQLite's text format (`_time_format=sqlite`) unless the DSN picks another.

## Type-Safe Queries

```go
// Define a model
type User struct {
    ID    int    `db:"id"`
    Email string `db:"email"`  
    Name  string `db:"name"`
}

// Type-safe queries
user, err := database.GetStruct[User](app.DB, "SELECT * FROM users WHERE id = ?", 1)
users, err := database.ListStruct[User](app.DB, "SELECT * FROM users")

// Model wrapper
userModel := database.NewModel[User](app.DB, "users")
user, err := userModel.Get(1)
users, err := userModel.Where("active = ?", true)
```

## Migrations

```go
// Table builder
migration := database.Table("users").
    ID().
    String("email").NotNull().Unique().
    String("name").NotNull().
    Boolean("active").Default(true).
    Timestamps().
    Build()

app.Migrate(migration)

// Table, column, and ForeignKey names must be plain identifiers; anything else panics.

// Common migrations
app.Migrate(
    database.CommonMigrations{}.Users(),
    database.CommonMigrations{}.Posts(),
    database.CommonMigrations{}.Sessions(),
)
```

## Query Builder

```go
// Fluent query building
query, args := database.Select("users.name", "posts.title").
    From("users").
    LeftJoin("posts", "users.id = posts.user_id").
    Where("users.active = ?", true).
    OrderBy("title", "DESC").
    Limit(10).
    Build()

results, err := app.DB.List(query, args...)
```

`From`, `Join`, `LeftJoin`, and `OrderBy` interpolate their table or column name, so each must
be a plain identifier (letters, digits, underscores; no alias, dot, or quote) and panics
otherwise. `OrderBy` takes only `ASC` or `DESC`. `Select` columns may be `*`, `table.*`,
`column`, `table.column`, or a `COUNT`/`SUM`/`AVG`/`MIN`/`MAX`/`TOTAL` aggregate such as
`COUNT(*)` or `COUNT(DISTINCT user_id)`, each but the stars optionally followed by `AS alias`;
anything else panics, so write other expressions as raw SQL with `DB.List`. `Where` and join
conditions are raw SQL: keep them static and pass values as `?` arguments.

## Pagination

```go
// Easy pagination
paginator, posts, err := database.Paginate(app.DB, "posts", 1, 10, "published = ?", true)

// Returns:
// - paginator.Page, paginator.TotalPages, etc.
// - posts slice with data
```

## Validation

Field rules belong in `validate` tags on the struct `hyperchi.Bind` fills. The one check that
needs the database, uniqueness, is `database.IsTaken`, usually called from the struct's
`Validate` method:

```go
type Signup struct {
    Email    string `form:"email" validate:"required,email"`
    Password string `form:"password" validate:"required,min=8"`
}

taken, err := database.IsTaken(app.DB, "users", "email", in.Email)
if err != nil {
    return err
}
if taken {
    return hyperchi.Unprocessable("Please correct the highlighted fields.",
        map[string][]string{"email": {"is already registered"}})
}
```

## Utilities

```go
// Slug generation (helpers.Slugify's rule: "Crème Brûlée" becomes "creme-brulee")
slug := database.SlugGenerator{}.Generate("My Blog Post")  // "my-blog-post"
uniqueSlug, err := database.SlugGenerator{}.UniqueSlug("My Post", "posts", "slug", app.DB)

// Backup
err := database.NewBackup(app.DB).Create("./backup.db")
size, err := database.NewBackup(app.DB).GetSize()

// Seeding
database.NewSeed(app.DB).AdminUser("admin@example.com", "password", "Admin")
database.NewSeed(app.DB).DefaultSettings()
```

## Raw SQL Access

```go
// Direct database access when needed
rows, err := app.DB.Query("SELECT * FROM complex_view WHERE condition = ?", value)
result, err := app.DB.Exec("INSERT INTO table ...")

// Transaction support  
err := app.DB.Tx(func(tx *database.Tx) error {
    _, err := tx.Create("users", userData)
    if err != nil {
        return err
    }
    _, err = tx.Create("profiles", profileData)
    return err
})
```

## PocketBase Optimizations

The database layer includes several performance optimizations inspired by PocketBase:

- **WAL Mode**: Better concurrency for read/write operations
- **Connection Pooling**: Efficient connection management
- **Optimized PRAGMA Settings**: Tuned for performance
- **Memory-Mapped I/O**: Faster file access
- **Prepared Statement Caching**: Reduced query compilation overhead

## Philosophy

This database layer follows HyperChi's microframework philosophy:

- **Essential Features Only**: No complex ORM features
- **Raw SQL Encouraged**: For anything beyond basic CRUD
- **Zero Magic**: Everything is explicit and obvious
- **Performance First**: Optimized for real-world usage
- **Simple API**: Easy to learn and use

For complex queries, relationships, or advanced ORM features, use raw SQL or dedicated libraries.
# HyperChi Framework Roadmap

> **Status: historical planning record.** The target APIs below are proposals, not the shipped
> API, and the timelines have lapsed. Phase 7 shipped as the `hyperchi/database` package used
> through `app.DB` (`app.DB.Get(&dest, query, args...)`, `database.NewModel[T]`), not
> `app.DB.Get[T]`. Phase 8 shipped as the SSE hub, `app.WebSocket` routes, and `app.Room`
> broadcasts (see `docs/CONTEXT_SYSTEM.md`); there is no `app.Live`. Of Phase 9, error handling
> and parsing shipped with the context-first API ([CONTEXT_FIRST_API.md](CONTEXT_FIRST_API.md));
> logging is `app.Helpers.Logger`, and `app.Safe` and `app.Log` do not exist. The root README's
> Roadmap section lists current plans.

## Vision: A Lightweight SSR + HTMX Microframework

HyperChi aims to be a lightweight, elegant microframework that makes building server-side rendered applications with HTMX effortless, without the bloat of full-stack frameworks.

## Core Philosophy: **Micro, Not Macro**

- **Single Import**: Everything through `import "github.com/regiellis/hyperchi/hyperchi"`
- **Minimal Core**: Essential features only, extensible through plugins
- **Zero Config**: Works out of the box, configure only what you need
- **Python-like DX**: Hide Go's rough edges with elegant abstractions
- **HTMX-First**: Designed around server-side rendering and HTMX patterns
- **Lightweight**: Flask simplicity, not Rails complexity
- **Modular**: Use only what you need, when you need it

---

## Phase 7: Lightweight SQLite Integration

### Goals: **Lightweight Database Layer, Not Complex ORM**
- **PocketBase-level optimizations** for SQLite performance
- **Simple, lightweight ORM** - essential features only
- **Zero configuration** database setup
- **Optional migrations** - use if you need them
- **Minimal query builder** - raw SQL encouraged for complex cases

### Target API: **Minimal & Optional**
```go
// Automatic setup - no config needed
app := hyperchi.New()

// OPTIONAL: Define models (or just use raw SQL)
type User struct {
    ID    int    `db:"id"`
    Email string `db:"email"`
    Name  string `db:"name"`
}

// Simple operations (optional - raw SQL works too)
user := app.DB.Get[User]("SELECT * FROM users WHERE id = ?", id)
users := app.DB.List[User]("SELECT * FROM users WHERE active = 1")
app.DB.Exec("INSERT INTO users (name, email) VALUES (?, ?)", name, email)

// Optional convenience methods
app.DB.Create("users", map[string]interface{}{"name": "John", "email": "john@example.com"})
app.DB.Update("users", 1, map[string]interface{}{"name": "Jane"})
app.DB.Delete("users", 1)

// Raw SQL is encouraged for anything complex
rows := app.DB.Query("SELECT u.*, p.bio FROM users u LEFT JOIN profiles p ON u.id = p.user_id")

// Optional transactions
app.DB.Tx(func(tx *DB) error {
    tx.Exec("INSERT INTO users ...")
    tx.Exec("INSERT INTO profiles ...")
    return nil // auto-commit
})
```

### PocketBase Optimizations to Include
- **Connection pooling** with proper lifecycle management
- **WAL mode** for concurrent reads/writes
- **Optimized PRAGMA settings** (journal_mode, synchronous, cache_size, etc.)
- **Prepared statement caching** for common queries
- **Smart indexing** with automatic index suggestions
- **Batch operations** for bulk inserts/updates
- **Query optimization** and explain plans in debug mode

### Implementation Priority: **Start Small, Stay Small**
1. Core SQLite wrapper with PocketBase optimizations
2. Basic CRUD helpers (Get, List, Exec, Create, Update, Delete)
3. Simple transaction support
4. Optional struct mapping
5. **STOP HERE** - No complex ORM features
6. Encourage raw SQL for anything beyond basics

---

## Phase 8: Minimal Live System

### Goals: **Simple Real-time, Not Complex Reactivity**
- **Basic real-time HTML updates** when data changes
- **Zero client-side complexity** - pure HTMX + SSE
- **Simple pub/sub** - no complex reactive patterns
- **Optional feature** - use only if you need real-time
- **Lightweight implementation** - minimal overhead

### Target API: **Simple & Optional**
```go
// Basic SSE endpoint
app.Live("/events", func(client *LiveClient) {
    // Send updates to this client
    client.Send("user-created", "<div>New user added</div>")
})

// Simple broadcasting
app.Live.Broadcast("user-list", htmlFragment)

// Manual event sending (no automatic reactivity)
app.Post("/users", func(c *hyperchi.Context) error {
    // Create user...
    app.Live.Broadcast("user-created", app.Template("user-row", user))
    return c.JSON(user)
})

// Optional: Simple channels
app.Live.Channel("dashboard").Send("metrics", htmlFragment)
```

### Features: **Essential Only**
- **SSE (Server-Sent Events)** for live updates
- **Basic channel system** for grouping clients
- **Simple broadcasting** to connected clients
- **HTMX integration** for seamless DOM updates
- **Optional WebSocket** support (if SSE isn't enough)

### Use Cases Enabled
- Live notifications and activity feeds
- Auto-updating data tables
- Simple real-time dashboards
- Basic chat/messaging
- **NOT**: Complex collaborative editing or multiplayer features

---

## Phase 9: Lightweight Developer Experience Helpers

### Goals: **Small Conveniences, Not Magic**
- **Hide common Go pain points** with simple helpers
- **Reduce error handling boilerplate** for web development
- **Optional conveniences** - can still use standard Go patterns
- **No performance overhead** - compile-time helpers where possible
- **Clear and explicit** - magic is bad, convenience is good

### Target Improvements

#### 1. Simple Error Handling
Shipped as part of the context-first handler API: a handler returns an error, and the app's
error handler renders it for the client. `HTTPError` constructors pick the status; any other
error is a 500 whose text is logged, never shown.

```go
app.Get("/users/{id}", func(c *hyperchi.Context) error {
    user, err := users.Find(c.Ctx(), c.Param("id"))
    if errors.Is(err, database.ErrNoRows) {
        return hyperchi.NotFound("No such user.")
    }
    if err != nil {
        return err
    }
    return c.View("user", hyperchi.H{"user": user})
})
```

#### 2. Basic Nil Safety
```go
// Simple nil-safe access (optional)
name := app.Safe(user, func(u *User) string { return u.Name }).OrDefault("Anonymous")

// Nil-safe templates (already works)
app.Template("user", user)  // handles nil gracefully
```

#### 3. Simple Logging
```go
// Basic structured logging
app.Log.Info("User created", "id", user.ID)
app.Log.Error("Database error", "error", err)

// Request logging (built-in middleware)
app.Use(app.Middleware.Logger())
```

#### 4. HTTP Parsing Helpers
Shipped as part of the context-first handler API: `hyperchi.Bind[T](c)` decodes and validates
input, and `c.ParamInt` returns a 400 error for a non-integer parameter.

```go
type CreateUserRequest struct {
    Name  string `json:"name" validate:"required"`
    Email string `json:"email" validate:"required,email"`
}

app.Post("/users", func(c *hyperchi.Context) error {
    req, err := hyperchi.Bind[CreateUserRequest](c) // 400 for invalid JSON, 422 for failed rules
    if err != nil {
        return err
    }
    // ... rest of handler
})

// Route parameter helpers
app.Get("/users/{id}", func(c *hyperchi.Context) error {
    id, err := c.ParamInt("id") // a 400 HTTPError when the id is not an integer
    if err != nil {
        return err
    }
    // ... rest of handler
})
```

### Implementation Notes: **Keep It Simple**
- **No magic** - everything should be obvious
- **Optional usage** - developers can still use verbose Go patterns
- **Zero overhead** - compile-time helpers, not runtime abstractions
- **Minimal surface area** - only the most common pain points
- **Don't reinvent Go** - enhance, don't replace

---

## Timeline and Priorities: **Microframework First**

### Phase 7 (Next 2-4 weeks)
1. **Basic SQLite Integration** - Core wrapper with PocketBase optimizations
2. **Essential CRUD helpers** - Get, List, Exec, Create, Update, Delete
3. **Stop here** - No complex ORM features

### Phase 8 (1-2 months)  
4. **Simple Live System** - SSE-based real-time updates
5. **Basic channels** - Group clients, simple broadcasting
6. **Stop here** - No complex reactive features

### Phase 9 (2-3 months)
7. **Developer experience helpers** - Error handling, parsing, logging
8. **Keep minimal** - Only most common pain points
9. **Stop here** - No magic or complex abstractions

### Always
- **Examples and documentation** - Keep updated with each phase
- **Performance testing** - Ensure microframework stays micro
- **Community feedback** - Stay lightweight based on real usage

---

## Success Metrics: **Microframework Success**

1. **Simplicity**: Build a basic HTMX app in under 50 lines of code
2. **Performance**: Match or exceed Gin/Echo performance
3. **Size**: Single binary under 10MB, minimal memory footprint
4. **Learning**: New developers productive within 30 minutes
5. **Adoption**: Become the go-to choice for simple Go web apps

---

## Anti-Goals: **What HyperChi Will NOT Become**

- ❌ **Complex ORM** - Use raw SQL for anything beyond basic CRUD
- ❌ **Full-stack framework** - No admin panels, user management, etc.
- ❌ **Magic** - No automatic code generation, complex metaprogramming
- ❌ **Enterprise features** - No complex caching, clustering, etc.
- ❌ **All-in-one** - No attempt to replace every Go web library

**HyperChi is lightweight, not complex. Simple, not comprehensive.**

---

*This roadmap is a living document and will evolve based on community feedback and real-world usage.*
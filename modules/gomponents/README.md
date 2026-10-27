# gomponents adapter

An optional module that renders [gomponents](https://www.gomponents.com) nodes
through HyperChi's integration manager and adds typed htmx and `hc-*`
attributes. The package name is `gomponentsadapter`, and the examples import it as
`gc`.

```go
import (
    "github.com/regiellis/hyperchi/hyperchi"
    "github.com/regiellis/hyperchi/hyperchi/helpers"
    gc "github.com/regiellis/hyperchi/modules/gomponents"
    g "maragu.dev/gomponents"
    h "maragu.dev/gomponents/html"
)

helpers.Must(app.Integrations().Register("gomponents", gc.New()))
app.Get("/lists/{id}", func(c *hyperchi.Context) error {
    id, err := c.ParamInt("id")
    if err != nil {
        return err
    }
    return c.RenderWith("gomponents", h.Main(
        h.H1(g.Text("Recipients")),
        h.Input(h.Type("search"), h.Name("q"),
            gc.HxGet(fmt.Sprintf("/lists/%d/recipients", id)),
            gc.HxTrigger("input changed delay:300ms, search"),
            gc.HxTarget("#recipients")),
        h.Div(h.ID("recipients")),
    ))
})
```

Import `github.com/regiellis/hyperchi/modules/gomponents`. It wraps
`maragu.dev/gomponents` **v1.3.0**, the latest release on the Go module proxy,
checked 2026-10-05.

## Rendering

`c.RenderWith(name, node)` takes any `gomponents.Node`. HyperChi buffers the
output, so a node that fails halfway sends a 500 and none of its markup. The
response is `text/html; charset=utf-8` with the status set by `c.Status`, so a
form that fails validation answers with
`c.Status(422).RenderWith("gomponents", form)`. A value that is not a node, a
nil node, or a request context that has already ended is an error. An empty
`g.Group` renders nothing and is not an error.

`gc.HTML(node)` renders a node to `template.HTML`, for mixing gomponents into
an `html/template` layout. gomponents escapes text and attribute values while
rendering, so the result is safe to print with `{{.}}` as long as the node
holds no `g.Raw` built from request input.

## htmx and directive attributes

Each helper returns an `Attribute` node with a fixed name and an escaped value:

| htmx | directives |
| --- | --- |
| `HxGet`, `HxPost`, `HxPut`, `HxPatch`, `HxDelete` | `HcData`, `HcOn(event, expr)` |
| `HxTarget`, `HxSwap`, `HxTrigger`, `HxSelect` | `HcText`, `HcShow`, `HcModel` |
| `HxInclude`, `HxIndicator`, `HxConfirm`, `HxVals` | `HcBind(attr, expr)`, `HcInit` |
| `HxPushURL`, `HxReplaceURL`, `HxSync`, `HxEncoding` | `HcRef`, `HcCloak()` |
| `HxBoost(bool)`, `HxOn(event, script)` | |
| `SSEConnect(url)`, `SSEClose(event)` | |

htmx 4 does not inherit attributes unless asked: `gc.HxTarget("#main").Inherited()`
renders `hx-target:inherited="#main"`. Only `hx-*` attributes can be inherited.

An event or attribute name becomes part of the attribute name, which HTML
cannot escape. `HxOn`, `HcOn`, `HcBind`, and `SSEClose` check it (letters,
digits, `_`, `.`, `:`, `-`), and an invalid one makes the render fail instead
of writing markup. Expressions and URLs are escaped like any attribute value.
They are still code the browser runs, so build them from your own strings, not
from request input.

```go
h.Div(
    gc.HcData("{open: false}"),
    h.Button(gc.HcOn("click", "open = !open"), g.Text("Details")),
    h.P(gc.HcShow("open"), gc.HcCloak(), g.Text("Hidden until opened.")),
)
```

The SSE helpers pair with `app.Extensions().EnableExtensionAssets()`, which
serves the htmx 4 extension at `/hyperchi/js/hx-sse.js`. Unnamed messages swap
into the element, and a named event such as `done` closes the stream:

```go
h.Div(gc.SSEConnect("/sendouts/7/events"), gc.SSEClose("done"), gc.HxSwap("innerHTML"))
```

## Testing

```sh
go vet ./... && go test -race -count=1 -cover ./... && golangci-lint run ./...
```

The examples in `example_test.go` compile with the package and match this
file. The integration tests build a HyperChi app from this checkout through a
local `replace`, so `go.mod` lists the core as a test dependency. The adapter
does not import it. `examples/mailgrid` renders all of its pages with this
module. Modules must be released with their own module-path version tags
before consumers can fetch those versions.

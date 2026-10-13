package gomponentsadapter_test

import (
	"fmt"
	"html/template"
	"os"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	gc "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

// SearchBox is an ordinary gomponents component using the typed htmx attributes.
func SearchBox(listID int, query string) g.Node {
	return h.Input(
		h.Type("search"), h.Name("q"), h.Value(query),
		gc.HxGet(fmt.Sprintf("/lists/%d/recipients", listID)),
		gc.HxTrigger("input changed delay:300ms, search"),
		gc.HxTarget("#recipients"),
	)
}

func Example() {
	app := hyperchi.New()
	helpers.Must(app.Integrations().Register("gomponents", gc.New()))
	app.Get("/lists/{id}", func(c *hyperchi.Context) error {
		id, err := c.ParamInt("id")
		if err != nil {
			return err
		}
		return c.RenderWith("gomponents", h.Main(
			h.H1(g.Text("Recipients")),
			SearchBox(id, c.Query("q")),
			h.Div(h.ID("recipients")),
		))
	})
	// app.Serve(":8080") starts the integration and serves the route.
}

func ExampleHTML() {
	body, err := gc.HTML(h.P(g.Text("Fish & chips")))
	if err != nil {
		panic(err)
	}
	layout := template.Must(template.New("layout").Parse(`<body>{{.}}</body>`))
	if err := layout.Execute(os.Stdout, body); err != nil {
		panic(err)
	}
	// Output: <body><p>Fish &amp; chips</p></body>
}

func ExampleHcOn() {
	fmt.Println(h.Div(
		gc.HcData("{open: false}"),
		h.Button(gc.HcOn("click", "open = !open"), g.Text("Details")),
		h.P(gc.HcShow("open"), gc.HcCloak(), g.Text("Hidden until opened.")),
	))
	// Output: <div hc-data="{open: false}"><button hc-on:click="open = !open">Details</button><p hc-show="open" hc-cloak>Hidden until opened.</p></div>
}

func ExampleSSEConnect() {
	fmt.Println(h.Div(
		gc.SSEConnect("/sendouts/7/events"), gc.SSEClose("done"), gc.HxSwap("innerHTML"),
		g.Text("Waiting for progress"),
	))
	// Output: <div hx-sse:connect="/sendouts/7/events" hx-sse:close="done" hx-swap="innerHTML">Waiting for progress</div>
}

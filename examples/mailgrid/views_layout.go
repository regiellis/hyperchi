package main

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
	gc "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

// pageMeta is what the layout needs besides the page content.
type pageMeta struct {
	Title   string
	Section string
	User    *User
	CSRF    string
}

// layout is the document every page shares: top bar, the outbox-only notice,
// and the main region. htmx and its SSE extension (served by the framework)
// load as blocking scripts so the extension is registered before htmx first
// processes the page. A deferred htmx initialises as soon as it runs, which
// would leave hx-sse:connect elements on the first page unprocessed. The
// directives and the app's own setup can wait for the parser.
func layout(m pageMeta, content ...g.Node) g.Node {
	return h.Doctype(
		h.HTML(h.Lang("en"),
			h.Head(
				h.Meta(h.Charset("utf-8")),
				h.Meta(h.Name("viewport"), h.Content("width=device-width, initial-scale=1")),
				h.TitleEl(g.Text(m.Title+" · Mailgrid")),
				h.Link(h.Rel("stylesheet"), h.Href("/static/css/app.css")),
				h.Link(h.Rel("icon"), h.Href("/static/favicon.svg"), h.Type("image/svg+xml")),
				h.Script(h.Src("/static/js/htmx.min.js")),
				h.Script(h.Src("/hyperchi/js/hx-sse.js")),
				h.Script(h.Src("/static/js/hyperchi-directives.js"), h.Defer()),
				h.Script(h.Src("/static/js/app.js"), h.Defer()),
			),
			h.Body(
				h.A(h.Class("skip-link"), h.Href("#main"), g.Text("Skip to content")),
				topBar(m),
				h.Div(h.Class("notice"), h.Role("note"),
					h.Strong(g.Text("Outbox only.")),
					g.Text(" Mailgrid renders letters into its outbox. It never sends email."),
				),
				h.Main(h.ID("main"), h.Class("container"), h.TabIndex("-1"), g.Group(content)),
			),
		),
	)
}

func topBar(m pageMeta) g.Node {
	link := func(section, href, label string) g.Node {
		return h.A(h.Href(href), g.If(m.Section == section, h.Aria("current", "page")), g.Text(label))
	}
	return h.Header(h.Class("topbar"),
		h.A(h.Class("brand"), h.Href("/lists"), g.Text("Mailgrid")),
		g.If(m.User != nil, g.Group{
			h.Nav(h.Aria("label", "Main"), h.Class("nav"),
				link("lists", "/lists", "Lists"),
				link("letters", "/letters", "Letters"),
				link("sendouts", "/sendouts", "Send-outs"),
				link("audit", "/audit", "Audit log"),
				link("team", "/team", "Team"),
			),
			h.Form(h.Class("signout"), h.Method("post"), h.Action("/logout"),
				gc.HxPost("/logout"),
				csrfField(m.CSRF),
				h.Span(h.Class("who"), g.Text(userLabel(m.User))),
				h.Button(h.Type("submit"), h.Class("btn btn-quiet"), g.Text("Sign out")),
			),
		}),
	)
}

func userLabel(u *User) string {
	if u == nil {
		return ""
	}
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func csrfField(token string) g.Node {
	return h.Input(h.Type("hidden"), h.Name("csrf_token"), h.Value(token))
}

func pageHeader(title string, actions ...g.Node) g.Node {
	return h.Div(h.Class("page-head"),
		h.H1(g.Text(title)),
		g.If(len(actions) > 0, h.Div(h.Class("actions"), g.Group(actions))),
	)
}

// field renders a labelled control with its error messages. The control
// must carry the same id.
func field(id, label string, control g.Node, errs []string, hint string) g.Node {
	return h.Div(h.Class("field"),
		h.Label(h.For(id), g.Text(label)),
		control,
		g.If(hint != "", h.P(h.Class("hint"), h.ID(id+"-hint"), g.Text(hint))),
		g.If(len(errs) > 0, h.P(h.Class("field-error"), h.ID(id+"-error"), g.Text(join(errs)))),
	)
}

// invalid marks a control that has errors and points to its message.
func invalid(id string, errs []string) g.Node {
	if len(errs) == 0 {
		return nil
	}
	return g.Group{h.Aria("invalid", "true"), h.Aria("describedby", id+"-error")}
}

func join(msgs []string) string {
	out := ""
	for i, m := range msgs {
		if i > 0 {
			out += " "
		}
		out += m
	}
	return out
}

func textInput(id, name, kind, value string, errs []string, extra ...g.Node) g.Node {
	return h.Input(h.ID(id), h.Name(name), h.Type(kind), h.Value(value), invalid(id, errs), g.Group(extra))
}

func errorAlert(he *hyperchi.HTTPError) g.Node {
	return h.Div(h.Class("alert alert-error"), h.Role("alert"),
		h.Strong(g.Text(statusTitle(he.Code))), g.Text(" "+he.Message))
}

func errorPage(he *hyperchi.HTTPError) g.Node {
	return h.Section(h.Class("panel narrow"),
		h.H1(g.Text(statusTitle(he.Code))),
		h.P(g.Text(he.Message)),
		h.P(h.A(h.Href("/lists"), g.Text("Back to lists"))),
	)
}

func statusTitle(code int) string {
	switch code {
	case 403:
		return "Not allowed."
	case 404:
		return "Not found."
	case 413:
		return "Too large."
	case 422:
		return "Check the form."
	}
	if code >= 500 {
		return "Server error."
	}
	return strconv.Itoa(code)
}

// pager renders previous and next links that keep the query. The links work
// as plain navigation; with a target, htmx swaps only that element.
func pager(p *database.Paginator, base string, query url.Values, target string) g.Node {
	if p.TotalPages <= 1 {
		return nil
	}
	link := func(page int, label string, enabled bool) g.Node {
		if !enabled {
			return h.Span(h.Class("pager-off"), h.Aria("disabled", "true"), g.Text(label))
		}
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		href := base + "?" + q.Encode()
		return h.A(h.Href(href), g.If(target != "", g.Group{gc.HxGet(href), gc.HxTarget(target), gc.HxSwap("outerHTML")}), g.Text(label))
	}
	return h.Nav(h.Class("pager"), h.Aria("label", "Pages"),
		link(p.Page-1, "Previous", p.HasPrev),
		h.Span(h.Class("num"), g.Textf("Page %d of %d", p.Page, p.TotalPages)),
		link(p.Page+1, "Next", p.HasNext),
	)
}

func when(t time.Time) g.Node {
	if t.IsZero() {
		return g.Text("")
	}
	local := t.Local()
	return h.Time(h.DateTime(local.Format(time.RFC3339)), h.Class("num"), g.Text(local.Format("2006-01-02 15:04")))
}

func whenPtr(t *time.Time) g.Node {
	if t == nil {
		return h.Span(h.Class("muted"), g.Text("not yet"))
	}
	return when(*t)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func badge(status string) g.Node {
	class := "badge"
	switch status {
	case statusDone, "rendered":
		class += " badge-ok"
	case statusRunning, "queued":
		class += " badge-live"
	case statusFailed, statusCancelled, statusInterrupted:
		class += " badge-warn"
	}
	return h.Span(h.Class(class), g.Text(statusLabel(status)))
}

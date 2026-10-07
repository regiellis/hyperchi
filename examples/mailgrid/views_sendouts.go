package main

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/regiellis/hyperchi/hyperchi/database"
	gc "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

func sendoutsView(rows []Progress) g.Node {
	return g.Group{
		pageHeader("Send-outs", h.A(h.Class("btn"), h.Href("/sendouts/new"), g.Text("New send-out"))),
		h.Div(h.Class("table-wrap"),
			h.Table(h.Class("data"),
				h.Caption(g.Text(plural(len(rows), "send-out", "send-outs"))),
				h.THead(h.Tr(
					h.Th(h.Scope("col"), h.Class("num"), g.Text("#")),
					h.Th(h.Scope("col"), g.Text("Letter")),
					h.Th(h.Scope("col"), g.Text("List")),
					h.Th(h.Scope("col"), g.Text("Status")),
					h.Th(h.Scope("col"), h.Class("num"), g.Text("Rendered")),
					h.Th(h.Scope("col"), h.Class("num"), g.Text("Failed")),
					h.Th(h.Scope("col"), g.Text("Started")),
				)),
				h.TBody(
					g.If(len(rows) == 0, h.Tr(h.Td(h.ColSpan("7"), h.Class("empty"), g.Text("Nothing has been rendered yet.")))),
					g.Map(rows, func(p Progress) g.Node {
						so := p.Sendout
						return h.Tr(
							h.Td(h.Class("num"), h.A(h.Href(fmt.Sprintf("/sendouts/%d", so.ID)), g.Text(strconv.FormatInt(so.ID, 10)))),
							h.Td(g.Text(so.LetterTitle)),
							h.Td(g.Text(so.ListName)),
							h.Td(badge(so.Status)),
							h.Td(h.Class("num"), g.Textf("%d / %d", p.Rendered, p.Total)),
							h.Td(h.Class("num"), g.Text(strconv.Itoa(p.Failed))),
							h.Td(whenPtr(so.StartedAt)),
						)
					}),
				),
			),
		),
	}
}

func newSendoutView(p SendoutPlan) g.Node {
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/sendouts"), g.Text("Send-outs")), g.Text(" / ")),
		pageHeader("New send-out"),
		h.Div(h.Class("grid-2"),
			h.Form(h.ID("plan-picker"), h.Class("panel"), h.Method("get"), h.Action("/sendouts/new"),
				gc.HxGet("/sendouts/new"), gc.HxTarget("#plan"), gc.HxSwap("outerHTML"), gc.HxTrigger("change"),
				h.H2(g.Text("Letter and list")),
				field("plan-letter", "Letter", h.Select(h.ID("plan-letter"), h.Name("letter_id"),
					h.Option(h.Value(""), g.Text("Choose a letter")),
					g.Map(p.Letters, func(l Letter) g.Node {
						return h.Option(h.Value(strconv.FormatInt(l.ID, 10)), g.If(p.Letter != nil && p.Letter.ID == l.ID, h.Selected()), g.Text(l.Title))
					}),
				), nil, ""),
				field("plan-list", "List", h.Select(h.ID("plan-list"), h.Name("list_id"),
					h.Option(h.Value(""), g.Text("Choose a list")),
					g.Map(p.Lists, func(l List) g.Node {
						return h.Option(h.Value(strconv.FormatInt(l.ID, 10)), g.If(p.List != nil && p.List.ID == l.ID, h.Selected()),
							g.Textf("%s (%d)", l.Name, l.Recipients))
					}),
				), nil, ""),
				h.NoScript(h.Button(h.Type("submit"), h.Class("btn btn-secondary"), g.Text("Check"))),
			),
			sendoutPlanPanel(p),
		),
	}
}

// sendoutPlanPanel says what starting will do and asks for the outbox-only
// confirmation. The start button stays disabled until the box is ticked; the
// server checks the box again.
func sendoutPlanPanel(p SendoutPlan) g.Node {
	if p.Letter == nil || p.List == nil {
		return h.Section(h.ID("plan"), h.Class("panel"), h.Aria("live", "polite"),
			h.H2(g.Text("What will happen")),
			h.P(h.Class("muted"), g.Text("Choose a letter and a list.")),
		)
	}
	return h.Section(h.ID("plan"), h.Class("panel"), h.Aria("live", "polite"),
		h.H2(g.Text("What will happen")),
		g.If(p.Problem != "", h.P(h.Class("alert alert-error"), h.Role("alert"), g.Text(p.Problem))),
		h.Dl(h.Class("kv"),
			h.Dt(g.Text("Letter")), h.Dd(h.A(h.Href(fmt.Sprintf("/letters/%d", p.Letter.ID)), g.Text(p.Letter.Title))),
			h.Dt(g.Text("List")), h.Dd(h.A(h.Href(fmt.Sprintf("/lists/%d", p.List.ID)), g.Text(p.List.Name))),
			h.Dt(g.Text("Recipients")), h.Dd(h.Class("num"), g.Text(strconv.Itoa(p.List.Recipients))),
			h.Dt(g.Text("Already in the outbox")), h.Dd(h.Class("num"), g.Text(strconv.Itoa(p.InOutbox))),
			h.Dt(g.Text("Will render now")), h.Dd(h.Class("num"), h.Strong(g.Text(strconv.Itoa(p.ToRender)))),
			h.Dt(g.Text("Merge fields")), h.Dd(h.Class("mono"), g.Text(fieldSummary(p.Fields))),
		),
		g.Map(p.Gaps, func(gap FieldGap) g.Node { return gapWarning(gap, p.List.Name, p.Letter.Strict) }),
		g.If(p.Existing != nil, h.P(h.Class("hint"),
			g.Text("This letter has a send-out to this list already. Starting again renders only recipients who are not in the outbox yet. "),
			h.A(h.Href(fmt.Sprintf("/sendouts/%d", existingID(p))), g.Text("Open it")), g.Text("."))),
		g.If(p.Running, h.P(h.Class("alert alert-info"), g.Text("It is rendering right now."))),
		h.Form(h.Method("post"), h.Action("/sendouts"), gc.HcData("{ok: false}"),
			gc.HxPost("/sendouts"), gc.HxTarget("#plan"), gc.HxSwap("outerHTML"),
			csrfField(p.CSRF),
			h.Input(h.Type("hidden"), h.Name("letter_id"), h.Value(strconv.FormatInt(p.Letter.ID, 10))),
			h.Input(h.Type("hidden"), h.Name("list_id"), h.Value(strconv.FormatInt(p.List.ID, 10))),
			h.Div(h.Class("field check confirm"),
				h.Input(h.ID("confirm-outbox"), h.Type("checkbox"), h.Name("confirm"), h.Value("outbox"), gc.HcModel("ok")),
				h.Label(h.For("confirm-outbox"),
					g.Text("I understand these letters go to the Mailgrid outbox. Nothing is emailed.")),
			),
			h.Button(h.Type("submit"), h.Class("btn"), gc.HcBind("disabled", "!ok"), g.Textf("Render %s into the outbox", plural(p.ToRender, "letter", "letters"))),
		),
	)
}

func existingID(p SendoutPlan) int64 {
	if p.Existing == nil {
		return 0
	}
	return p.Existing.ID
}

func sendoutView(p Progress, csrf string) g.Node {
	so := p.Sendout
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/sendouts"), g.Text("Send-outs")), g.Text(" / ")),
		pageHeader(fmt.Sprintf("Send-out %d", so.ID),
			h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/%d/outbox", so.ID)), g.Text("Browse outbox")),
			h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/%d/export.csv", so.ID)), h.Download(""), g.Text("Export CSV")),
			h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/%d/export.zip", so.ID)), h.Download(""), g.Text("Export .eml (zip)")),
		),
		h.Dl(h.Class("kv inline"),
			h.Dt(g.Text("Letter")), h.Dd(h.A(h.Href(fmt.Sprintf("/letters/%d", so.LetterID)), g.Text(so.LetterTitle))),
			h.Dt(g.Text("List")), h.Dd(h.A(h.Href(fmt.Sprintf("/lists/%d", so.ListID)), g.Text(so.ListName))),
			h.Dt(g.Text("Delivery")), h.Dd(g.Text("None. Letters are written to the outbox only.")),
		),
		// The stream only opens while the job runs. Its first message is the
		// state in the database, so a reload picks up where the job is.
		h.Div(h.ID("progress"), h.Class("progress-region"),
			g.If(p.Running(), g.Group{
				gc.SSEConnect(fmt.Sprintf("/sendouts/%d/events", so.ID)), gc.SSEClose("done"), gc.HxSwap("innerHTML"),
			}),
			progressPanel(p),
		),
		h.Div(h.Class("form-actions"),
			h.Form(h.Method("post"), h.Action("/sendouts"), h.ID("rerun"),
				gc.HxPost("/sendouts"),
				csrfField(csrf),
				h.Input(h.Type("hidden"), h.Name("letter_id"), h.Value(strconv.FormatInt(so.LetterID, 10))),
				h.Input(h.Type("hidden"), h.Name("list_id"), h.Value(strconv.FormatInt(so.ListID, 10))),
				h.Input(h.Type("hidden"), h.Name("confirm"), h.Value("outbox")),
			),
		),
	}
}

// progressPanel is swapped in whole by every SSE message. Its controls
// match the state: Cancel while running, Run again otherwise.
func progressPanel(p Progress) g.Node {
	so := p.Sendout
	pct := 0
	if p.Total > 0 {
		pct = p.Done() * 100 / p.Total
	}
	return h.Section(h.Class("panel"), h.Aria("labelledby", "progress-title"),
		h.Div(h.Class("progress-head"),
			h.H2(h.ID("progress-title"), g.Text("Progress")),
			badge(so.Status),
			g.If(p.Running(), h.Span(h.Class("live-dot"), h.Aria("hidden", "true"))),
		),
		h.P(h.Class("progress-line"), h.Role("status"),
			h.Strong(h.Class("num"), g.Textf("%d of %d", p.Done(), p.Total)),
			g.Text(" recipients done"),
			g.If(p.Failed > 0, g.Textf(", %d failed", p.Failed)),
			g.If(p.Queued > 0 && !p.Running(), g.Textf(", %d still queued", p.Queued)),
			g.Text("."),
		),
		h.Progress(h.Max(strconv.Itoa(max(p.Total, 1))), h.Value(strconv.Itoa(p.Done())), h.Aria("label", "Rendered"), g.Textf("%d%%", pct)),
		h.Dl(h.Class("kv counts"),
			h.Dt(g.Text("Rendered")), h.Dd(h.Class("num"), g.Text(strconv.Itoa(p.Rendered))),
			h.Dt(g.Text("Failed")), h.Dd(h.Class("num"), g.Text(strconv.Itoa(p.Failed))),
			h.Dt(g.Text("Queued")), h.Dd(h.Class("num"), g.Text(strconv.Itoa(p.Queued))),
			h.Dt(g.Text("Current")), h.Dd(h.Class("mono"), g.If(so.CurrentEmail == "", g.Text("none")), g.Text(so.CurrentEmail)),
		),
		g.If(so.Error != "", h.P(h.Class("alert alert-error"), g.Text(so.Error))),
		g.If(len(p.Failures) > 0, h.Div(
			h.H3(g.Text("Latest failures")),
			h.Ul(h.Class("plain failures"), g.Map(p.Failures, func(it OutboxItem) g.Node {
				return h.Li(h.Span(h.Class("mono"), g.Text(it.Email)), g.Text(": "+it.Error))
			})),
		)),
		h.Div(h.Class("form-actions"),
			g.If(p.Running(), h.Button(h.Type("button"), h.Class("btn btn-danger"),
				gc.HxPost(fmt.Sprintf("/sendouts/%d/cancel", so.ID)), gc.HxSwap("none"),
				g.Text("Cancel send-out"))),
			g.If(!p.Running() && so.Status != statusNew, h.Button(h.Type("submit"), h.Class("btn btn-secondary"), h.FormAttr("rerun"),
				g.Text(rerunLabel(p)))),
		),
	)
}

func rerunLabel(p Progress) string {
	if p.Queued > 0 {
		return fmt.Sprintf("Resume (%d queued)", p.Queued)
	}
	return "Run again for new recipients"
}

func outboxView(p Progress, status string, table g.Node) g.Node {
	so := p.Sendout
	tab := func(value, label string, n int) g.Node {
		href := fmt.Sprintf("/sendouts/%d/outbox", so.ID)
		if value != "" {
			href += "?status=" + value
		}
		return h.A(h.Href(href), g.If(status == value, h.Aria("current", "page")), g.Textf("%s (%d)", label, n))
	}
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/sendouts"), g.Text("Send-outs")), g.Text(" / "),
			h.A(h.Href(fmt.Sprintf("/sendouts/%d", so.ID)), g.Textf("Send-out %d", so.ID)), g.Text(" / ")),
		pageHeader("Outbox: "+so.LetterTitle+" to "+so.ListName,
			h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/%d/export.csv", so.ID)), h.Download(""), g.Text("Export CSV")),
			h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/%d/export.zip", so.ID)), h.Download(""), g.Text("Export .eml (zip)")),
		),
		h.Nav(h.Class("tabs"), h.Aria("label", "Filter by status"),
			tab("", "All", p.Total), tab("rendered", "Rendered", p.Rendered),
			tab("failed", "Failed", p.Failed), tab("queued", "Queued", p.Queued),
		),
		table,
	}
}

func outboxTable(so Sendout, status string, p *database.Paginator, items []OutboxItem) g.Node {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	return h.Div(h.ID("outbox-table"),
		h.Div(h.Class("table-wrap"),
			h.Table(h.Class("data"),
				h.Caption(g.Text(plural(p.Total, "letter", "letters"))),
				h.THead(h.Tr(
					h.Th(h.Scope("col"), h.Class("num"), g.Text("#")),
					h.Th(h.Scope("col"), g.Text("Recipient")),
					h.Th(h.Scope("col"), g.Text("Subject")),
					h.Th(h.Scope("col"), g.Text("Status")),
					h.Th(h.Scope("col"), g.Text("Rendered")),
				)),
				h.TBody(
					g.If(len(items) == 0, h.Tr(h.Td(h.ColSpan("5"), h.Class("empty"), g.Text("No letters here.")))),
					g.Map(items, func(it OutboxItem) g.Node {
						return h.Tr(
							h.Td(h.Class("num"), h.A(h.Href(fmt.Sprintf("/outbox/%d", it.ID)), g.Text(strconv.FormatInt(it.ID, 10)))),
							h.Td(h.Span(h.Class("mono"), g.Text(it.Email)), g.If(it.Name != "", h.Span(h.Class("muted"), g.Text(" "+it.Name)))),
							h.Td(g.If(it.Status == "rendered", h.A(h.Href(fmt.Sprintf("/outbox/%d", it.ID)), g.Text(it.Subject))),
								g.If(it.Status == "failed", h.Span(h.Class("warn"), g.Text(it.Error)))),
							h.Td(badge(it.Status)),
							h.Td(whenPtr(it.RenderedAt)),
						)
					}),
				),
			),
		),
		pager(p, fmt.Sprintf("/sendouts/%d/outbox", so.ID), query, "#outbox-table"),
	)
}

func outboxItemView(so Sendout, it OutboxItem, from string) g.Node {
	to := it.Email
	if it.Name != "" {
		to = it.Name + " <" + it.Email + ">"
	}
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/sendouts"), g.Text("Send-outs")), g.Text(" / "),
			h.A(h.Href(fmt.Sprintf("/sendouts/%d/outbox", so.ID)), g.Textf("Outbox of send-out %d", so.ID)), g.Text(" / ")),
		pageHeader(fmt.Sprintf("Letter %d", it.ID),
			g.If(it.Status == "rendered", h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/outbox/%d/eml", it.ID)), h.Download(""), g.Text("Download .eml"))),
		),
		h.Section(h.Class("panel"),
			h.Dl(h.Class("kv"),
				h.Dt(g.Text("Status")), h.Dd(badge(it.Status), g.Text(" Not emailed.")),
				h.Dt(g.Text("From")), h.Dd(h.Class("mono"), g.Text(from)),
				h.Dt(g.Text("To")), h.Dd(h.Class("mono"), g.Text(to)),
				h.Dt(g.Text("Subject")), h.Dd(h.Class("subject"), g.Text(it.Subject)),
				h.Dt(g.Text("Rendered")), h.Dd(whenPtr(it.RenderedAt)),
				g.If(it.Error != "", g.Group{h.Dt(g.Text("Problem")), h.Dd(h.Class("warn"), g.Text(it.Error))}),
			),
			g.If(it.Status == "rendered" && it.Format == formatHTML, h.IFrame(h.Class("letter-frame"), h.Title("Rendered letter"),
				g.Attr("sandbox", ""), g.Attr("srcdoc", it.Body))),
			g.If(it.Status == "rendered" && it.Format != formatHTML, h.Pre(h.Class("letter-text"), g.Text(it.Body))),
		),
	}
}

func auditView(p *database.Paginator, entries []AuditEntry) g.Node {
	return g.Group{
		pageHeader("Audit log"),
		h.Div(h.Class("table-wrap"),
			h.Table(h.Class("data"),
				h.Caption(g.Text(plural(p.Total, "entry", "entries"))),
				h.THead(h.Tr(
					h.Th(h.Scope("col"), g.Text("When")),
					h.Th(h.Scope("col"), g.Text("Who")),
					h.Th(h.Scope("col"), g.Text("What")),
					h.Th(h.Scope("col"), g.Text("Detail")),
				)),
				h.TBody(g.Map(entries, func(e AuditEntry) g.Node {
					who := e.UserEmail
					if who == "" {
						who = "system"
					}
					return h.Tr(h.Td(when(e.CreatedAt)), h.Td(h.Class("mono"), g.Text(who)), h.Td(g.Text(e.Action)), h.Td(g.Text(e.Detail)))
				})),
			),
		),
		pager(p, "/audit", url.Values{}, ""),
	}
}

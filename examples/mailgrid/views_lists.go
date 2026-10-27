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

func listsView(lists []List, in NewList, fields map[string][]string, csrf string) g.Node {
	return g.Group{
		pageHeader("Lists"),
		h.Div(h.Class("grid-2"),
			h.Div(h.Class("table-wrap"),
				h.Table(h.Class("data"),
					h.Caption(g.Text(plural(len(lists), "list", "lists"))),
					h.THead(h.Tr(
						h.Th(h.Scope("col"), g.Text("Name")),
						h.Th(h.Scope("col"), h.Class("num"), g.Text("Recipients")),
						h.Th(h.Scope("col"), g.Text("Created")),
					)),
					h.TBody(
						g.If(len(lists) == 0, h.Tr(h.Td(h.ColSpan("3"), h.Class("empty"), g.Text("No lists yet. Create one, then import a CSV.")))),
						g.Map(lists, func(l List) g.Node {
							return h.Tr(
								h.Td(h.A(h.Href(fmt.Sprintf("/lists/%d", l.ID)), g.Text(l.Name))),
								h.Td(h.Class("num"), g.Text(strconv.Itoa(l.Recipients))),
								h.Td(when(l.CreatedAt)),
							)
						}),
					),
				),
			),
			h.Section(h.Class("panel"), h.Aria("labelledby", "new-list"),
				h.H2(h.ID("new-list"), g.Text("New list")),
				newListForm(in, fields, csrf),
			),
		),
	}
}

func newListForm(in NewList, fields map[string][]string, csrf string) g.Node {
	return h.Form(h.ID("list-form"), h.Method("post"), h.Action("/lists"),
		gc.HxPost("/lists"), gc.HxTarget("this"), gc.HxSwap("outerHTML"),
		csrfField(csrf),
		field("list-name", "Name", textInput("list-name", "name", "text", in.Name, fields["name"],
			h.Required(), h.MaxLength("120")), fields["name"], ""),
		h.Button(h.Type("submit"), h.Class("btn"), g.Text("Create list")),
	)
}

func listView(l List, fields []string, maxUpload int64, table g.Node, csrf string) g.Node {
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/lists"), g.Text("Lists")), g.Text(" / ")),
		pageHeader(l.Name,
			h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/new?list_id=%d", l.ID)), g.Text("Send a letter to this list")),
		),
		h.Div(h.Class("grid-side"),
			h.Div(
				h.Div(h.Class("toolbar"),
					h.Label(h.For("recipient-search"), h.Class("sr-only"), g.Text("Search recipients")),
					h.Input(h.ID("recipient-search"), h.Type("search"), h.Name("q"), h.Placeholder("Search email or name"),
						h.AutoComplete("off"),
						gc.HxGet(fmt.Sprintf("/lists/%d", l.ID)),
						gc.HxTrigger("input changed delay:300ms, search"),
						gc.HxTarget("#recipient-table"), gc.HxSwap("outerHTML"),
						gc.HxSync("this:replace"),
					),
				),
				h.Div(h.ID("editor")),
				table,
			),
			h.Aside(h.Class("side"),
				h.Section(h.Class("panel"), h.Aria("labelledby", "import-title"),
					h.H2(h.ID("import-title"), g.Text("Import from CSV")),
					h.P(h.Class("hint"), g.Textf("The first row names the columns. email is required and name is optional. "+
						"Every other column becomes a merge field. Up to %s.", humanBytes(maxUpload))),
					h.Form(h.ID("import-form"), h.Method("post"), h.Action(fmt.Sprintf("/lists/%d/import", l.ID)),
						h.EncType("multipart/form-data"),
						gc.HxPost(fmt.Sprintf("/lists/%d/import", l.ID)), gc.HxTarget("#import-result"),
						gc.HxSwap("innerHTML"), gc.HxIndicator("#import-busy"),
						csrfField(csrf),
						field("import-file", "CSV file", h.Input(h.ID("import-file"), h.Type("file"), h.Name("file"),
							h.Accept(".csv,text/csv"), h.Required()), nil, ""),
						h.Button(h.Type("submit"), h.Class("btn"), g.Text("Import")),
						h.Span(h.ID("import-busy"), h.Class("busy"), g.Text("Importing…")),
					),
					h.Div(h.ID("import-result"), h.Aria("live", "polite")),
				),
				h.Section(h.Class("panel"), h.Aria("labelledby", "fields-title"),
					h.H2(h.ID("fields-title"), g.Text("Merge fields")),
					h.P(h.Class("hint"), g.Text("Use these in a letter as {{field}}.")),
					h.Ul(h.Class("chips"), g.Map(fields, func(f string) g.Node {
						return h.Li(h.Code(g.Text("{{" + f + "}}")))
					})),
				),
			),
		),
	}
}

// recipientTable is the swappable table: search, paging, and changes all
// replace it whole. It reloads itself when anything fires recipients-changed.
func recipientTable(l List, q string, p *database.Paginator, rows []Recipient) g.Node {
	base := fmt.Sprintf("/lists/%d", l.ID)
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	reload := base + "?" + url.Values{"q": {q}, "page": {strconv.Itoa(p.Page)}}.Encode()
	caption := plural(p.Total, "recipient", "recipients")
	if q != "" {
		caption = fmt.Sprintf("%s matching %q", caption, q)
	}
	return h.Div(h.ID("recipient-table"),
		gc.HxGet(reload), gc.HxTrigger("recipients-changed from:body"), gc.HxSwap("outerHTML"),
		h.Div(h.Class("table-wrap"),
			h.Table(h.Class("data"),
				h.Caption(g.Text(caption)),
				h.THead(h.Tr(
					h.Th(h.Scope("col"), g.Text("Email")),
					h.Th(h.Scope("col"), g.Text("Name")),
					h.Th(h.Scope("col"), g.Text("Merge fields")),
					h.Th(h.Scope("col"), h.Class("actions-col"), h.Span(h.Class("sr-only"), g.Text("Actions"))),
				)),
				h.TBody(
					g.If(len(rows) == 0, h.Tr(h.Td(h.ColSpan("4"), h.Class("empty"), g.Text(emptyRecipients(q))))),
					g.Map(rows, func(r Recipient) g.Node {
						fields, err := r.Fields()
						summary := ""
						if err != nil {
							summary = "unreadable"
						}
						for _, f := range fieldList(fields) {
							if summary != "" {
								summary += " · "
							}
							summary += f.Key + ": " + f.Value
						}
						return h.Tr(
							h.Td(h.Class("mono"), g.Text(r.Email)),
							h.Td(g.Text(r.Name)),
							h.Td(h.Class("fields-cell"), g.Text(summary)),
							h.Td(h.Class("row-actions"),
								h.Button(h.Type("button"), h.Class("btn-link"),
									gc.HxGet(fmt.Sprintf("/recipients/%d/edit", r.ID)), gc.HxTarget("#editor"), gc.HxSwap("innerHTML"),
									h.Aria("label", "Edit "+r.Email), g.Text("Edit")),
								h.Button(h.Type("button"), h.Class("btn-link danger"),
									gc.HxDelete(fmt.Sprintf("/recipients/%d", r.ID)), gc.HxSwap("none"),
									gc.HxConfirm("Remove "+r.Email+" from this list?"),
									h.Aria("label", "Remove "+r.Email), g.Text("Remove")),
							),
						)
					}),
				),
			),
		),
		pager(p, base, query, "#recipient-table"),
	)
}

func emptyRecipients(q string) string {
	if q != "" {
		return "No recipient matches that search."
	}
	return "No recipients yet. Import a CSV to add some."
}

func importResult(l List, r ImportReport) g.Node {
	if r.Parsed == nil {
		return h.Div(h.Class("alert alert-error"), h.Role("alert"), g.Text(r.Message))
	}
	p := r.Parsed
	return h.Div(h.Class("import-report"),
		h.Div(h.Class("alert alert-ok"), h.Role("status"),
			h.Strong(g.Textf("%s added.", plural(r.Added, "recipient", "recipients"))),
			g.If(r.Existing > 0, g.Textf(" %d already on %s, left unchanged.", r.Existing, l.Name)),
			g.If(len(p.Duplicates) > 0, g.Textf(" %s in the file skipped.", plural(len(p.Duplicates), "duplicate", "duplicates"))),
			g.If(len(p.Problems) > 0, g.Textf(" %s rejected.", plural(len(p.Problems), "row", "rows"))),
		),
		g.If(len(p.Columns) > 0, h.P(h.Class("hint"), g.Text("Merge fields in this file: "),
			g.Map(p.Columns, func(c string) g.Node { return h.Code(g.Text("{{" + c + "}} ")) }))),
		g.If(len(p.Problems)+len(p.Duplicates) > 0, h.Details(h.Open(),
			h.Summary(g.Text("Rows that were not imported")),
			h.Div(h.Class("table-wrap"),
				h.Table(h.Class("data compact"),
					h.Caption(h.Class("sr-only"), g.Text("Rows that were not imported")),
					h.THead(h.Tr(h.Th(h.Scope("col"), h.Class("num"), g.Text("Line")),
						h.Th(h.Scope("col"), g.Text("Email")), h.Th(h.Scope("col"), g.Text("Problem")))),
					h.TBody(
						g.Map(append(append([]ImportProblem{}, p.Problems...), p.Duplicates...), func(pr ImportProblem) g.Node {
							return h.Tr(h.Td(h.Class("num"), g.Text(strconv.Itoa(pr.Line))),
								h.Td(h.Class("mono"), g.Text(pr.Email)), h.Td(g.Text(pr.Message)))
						}),
					),
				),
			),
			g.If(p.Truncated, h.P(h.Class("hint"), g.Textf("Only the first %d problems are listed.", maxImportErrors))),
		)),
	)
}

func recipientEditor(r Recipient, fields []FieldValue, problems map[string][]string, csrf string) g.Node {
	action := fmt.Sprintf("/recipients/%d", r.ID)
	return h.Section(h.Class("panel editor"), h.Aria("labelledby", "editor-title"), gc.HcData("{}"),
		h.H2(h.ID("editor-title"), g.Text("Edit recipient")),
		h.Form(h.Method("post"), h.Action(action),
			gc.HxPost(action), gc.HxTarget("#editor"), gc.HxSwap("innerHTML"),
			csrfField(csrf),
			h.Div(h.Class("form-row"),
				field("edit-email", "Email", textInput("edit-email", "email", "email", r.Email, problems["email"], h.Required()), problems["email"], ""),
				field("edit-name", "Name", textInput("edit-name", "name", "text", r.Name, problems["name"]), problems["name"], ""),
				g.Map(fields, func(f FieldValue) g.Node {
					fid := "edit-field-" + f.Key
					return field(fid, f.Key, textInput(fid, "field."+f.Key, "text", f.Value, problems["field."+f.Key]), problems["field."+f.Key], "")
				}),
			),
			h.Div(h.Class("form-actions"),
				h.Button(h.Type("submit"), h.Class("btn"), g.Text("Save")),
				h.Button(h.Type("button"), h.Class("btn btn-quiet"), gc.HcOn("click", "$event.target.closest('#editor').replaceChildren()"), g.Text("Close")),
			),
		),
	)
}

func editorClosed(email string) g.Node {
	return h.P(h.Class("alert alert-ok"), h.Role("status"), g.Textf("Saved %s.", email))
}

// recipientSelectOptions fills the preview's recipient picker.
func recipientSelectOptions(rows []Recipient, selected int64) g.Node {
	if len(rows) == 0 {
		return h.Option(h.Value(""), g.Text("No recipients"))
	}
	return g.Map(rows, func(r Recipient) g.Node {
		label := r.Email
		if r.Name != "" {
			label = r.Name + " <" + r.Email + ">"
		}
		return h.Option(h.Value(strconv.FormatInt(r.ID, 10)), g.If(r.ID == selected, h.Selected()), g.Text(label))
	})
}

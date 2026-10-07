package main

import (
	"fmt"
	"strconv"

	gc "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

func lettersView(letters []Letter) g.Node {
	return g.Group{
		pageHeader("Letters", h.A(h.Class("btn"), h.Href("/letters/new"), g.Text("New letter"))),
		h.Div(h.Class("table-wrap"),
			h.Table(h.Class("data"),
				h.Caption(g.Text(plural(len(letters), "letter", "letters"))),
				h.THead(h.Tr(
					h.Th(h.Scope("col"), g.Text("Title")),
					h.Th(h.Scope("col"), g.Text("Subject")),
					h.Th(h.Scope("col"), g.Text("Format")),
					h.Th(h.Scope("col"), g.Text("Merge fields")),
					h.Th(h.Scope("col"), g.Text("Updated")),
				)),
				h.TBody(
					g.If(len(letters) == 0, h.Tr(h.Td(h.ColSpan("5"), h.Class("empty"), g.Text("No letters yet.")))),
					g.Map(letters, func(l Letter) g.Node {
						return h.Tr(
							h.Td(h.A(h.Href(fmt.Sprintf("/letters/%d", l.ID)), g.Text(l.Title))),
							h.Td(g.Text(l.Subject)),
							h.Td(g.Text(formatLabel(l.Format))),
							h.Td(h.Class("mono"), g.Text(fieldSummary(mergeFields(l.Subject, l.Body)))),
							h.Td(when(l.UpdatedAt)),
						)
					}),
				),
			),
		),
	}
}

func formatLabel(f string) string {
	if f == formatHTML {
		return "HTML"
	}
	return "Plain text"
}

func fieldSummary(fields []string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += " "
		}
		out += "{{" + f + "}}"
	}
	if out == "" {
		return "none"
	}
	return out
}

func composeView(letterID int64, form LetterForm, fields map[string][]string, lists []List, csrf string) g.Node {
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/letters"), g.Text("Letters")), g.Text(" / ")),
		pageHeader("New letter"),
		composer(letterID, form, fields, lists, csrf),
	}
}

// composer holds the form and the live preview. The wrapper issues the
// preview request: input and change events from the form bubble up to it,
// and it survives the form being swapped after a failed save.
func composer(letterID int64, form LetterForm, fields map[string][]string, lists []List, csrf string) g.Node {
	return h.Div(h.ID("composer"), h.Class("grid-compose"),
		gc.HxPost("/letters/preview"), gc.HxInclude("#letter-form"), gc.HxTarget("#preview"),
		gc.HxSwap("innerHTML"), gc.HxTrigger("load, input delay:400ms, change"), gc.HxSync("this:replace"),
		letterForm(letterID, form, fields, lists, csrf),
		h.Section(h.Class("panel preview"), h.Aria("labelledby", "preview-title"),
			h.H2(h.ID("preview-title"), g.Text("Preview")),
			h.Div(h.ID("preview"), h.Aria("live", "polite"), h.P(h.Class("muted"), g.Text("Loading preview…"))),
		),
	)
}

func letterForm(letterID int64, in LetterForm, fields map[string][]string, lists []List, csrf string) g.Node {
	action, button := "/letters", "Save letter"
	if letterID > 0 {
		action, button = fmt.Sprintf("/letters/%d", letterID), "Save changes"
	}
	return h.Form(h.ID("letter-form"), h.Class("panel"), h.Method("post"), h.Action(action),
		gc.HxPost(action), gc.HxTarget("this"), gc.HxSwap("outerHTML"),
		csrfField(csrf),
		field("letter-title", "Title", textInput("letter-title", "title", "text", in.Title, fields["title"],
			h.Required(), h.MaxLength("120"), h.Aria("describedby", "letter-title-hint")), fields["title"],
			"For the team. Recipients never see it."),
		field("letter-subject", "Subject", textInput("letter-subject", "subject", "text", in.Subject, fields["subject"],
			h.Required(), h.MaxLength("200")), fields["subject"], ""),
		h.Div(h.Class("form-row"),
			field("letter-format", "Format", h.Select(h.ID("letter-format"), h.Name("format"),
				h.Option(h.Value(formatText), g.If(in.Format != formatHTML, h.Selected()), g.Text("Plain text")),
				h.Option(h.Value(formatHTML), g.If(in.Format == formatHTML, h.Selected()), g.Text("HTML")),
			), fields["format"], ""),
			h.Div(h.Class("field check"),
				h.Input(h.ID("letter-strict"), h.Type("checkbox"), h.Name("strict"), h.Value("true"), g.If(in.Strict, h.Checked()),
					h.Aria("describedby", "letter-strict-hint")),
				h.Label(h.For("letter-strict"), g.Text("Hold letters with an empty merge field")),
				h.P(h.Class("hint"), h.ID("letter-strict-hint"), g.Text("They are marked failed in the outbox instead of rendering with a blank.")),
			),
		),
		field("letter-body", "Body", h.Textarea(h.ID("letter-body"), h.Name("body"), h.Rows("16"), h.Required(),
			h.SpellCheck("true"), invalid("letter-body", fields["body"]), h.Aria("describedby", "letter-body-hint"), g.Text(in.Body)),
			fields["body"], "Insert a merge field as {{name}}. In an HTML letter, merged values are escaped."),
		h.FieldSet(h.Class("preview-controls"),
			h.Legend(g.Text("Preview as")),
			h.Div(h.Class("form-row"),
				field("preview-list", "List", h.Select(h.ID("preview-list"), h.Name("list_id"),
					gc.HxGet("/recipient-options"), gc.HxInclude("this"), gc.HxTarget("#preview-recipient"),
					gc.HxTrigger("change"), gc.HxSwap("innerHTML"),
					h.Option(h.Value(""), g.Text("No list")),
					g.Map(lists, func(l List) g.Node {
						return h.Option(h.Value(strconv.FormatInt(l.ID, 10)), g.Text(l.Name))
					}),
				), nil, ""),
				field("preview-recipient", "Recipient", h.Select(h.ID("preview-recipient"), h.Name("recipient_id"),
					h.Option(h.Value(""), g.Text("Choose a list first")),
				), nil, ""),
			),
		),
		h.Div(h.Class("form-actions"),
			h.Button(h.Type("submit"), h.Class("btn"), g.Text(button)),
			g.If(letterID > 0, h.A(h.Class("btn btn-secondary"), h.Href(fmt.Sprintf("/sendouts/new?letter_id=%d", letterID)), g.Text("Send out"))),
		),
	)
}

func letterView(l Letter, form LetterForm, lists []List, sendouts []Sendout, csrf string) g.Node {
	return g.Group{
		h.P(h.Class("crumbs"), h.A(h.Href("/letters"), g.Text("Letters")), g.Text(" / ")),
		pageHeader(l.Title),
		g.If(len(sendouts) > 0, h.P(h.Class("alert alert-info"),
			g.Text("This letter has send-outs. Editing it changes future renders only; letters already in the outbox stay as they were."))),
		composer(l.ID, form, nil, lists, csrf),
		h.Section(h.Class("panel"), h.Aria("labelledby", "history-title"),
			h.H2(h.ID("history-title"), g.Text("Send-outs of this letter")),
			g.If(len(sendouts) == 0, h.P(h.Class("muted"), g.Text("Not sent to any list yet."))),
			g.If(len(sendouts) > 0, h.Ul(h.Class("plain"), g.Map(sendouts, func(so Sendout) g.Node {
				return h.Li(h.A(h.Href(fmt.Sprintf("/sendouts/%d", so.ID)), g.Text(so.ListName)), g.Text(" "), badge(so.Status))
			}))),
		),
	}
}

func previewPanel(p Preview) g.Node {
	return g.Group{
		h.P(h.Class("preview-for"),
			g.Iff(p.Recipient != nil, func() g.Node { return g.Group{g.Text("For "), h.Strong(g.Text(recipientLabel(p.Recipient)))} }),
			g.If(p.Recipient == nil && p.List != nil, g.Text("This list has no recipients, so merge fields render empty.")),
			g.If(p.Recipient == nil && p.List == nil, g.Text("Pick a list to preview a real recipient. Merge fields render empty until then.")),
		),
		h.Dl(h.Class("kv"),
			h.Dt(g.Text("Subject")), h.Dd(h.Class("subject"), g.Text(p.Merged.Subject)),
		),
		g.If(p.Format == formatHTML, h.IFrame(h.Class("letter-frame"), h.Title("Letter preview"),
			g.Attr("sandbox", ""), g.Attr("srcdoc", p.Merged.Body))),
		g.If(p.Format != formatHTML, h.Pre(h.Class("letter-text"), g.Text(p.Merged.Body))),
		h.Div(h.Class("checks"),
			h.P(g.Text("Merge fields: "), h.Span(h.Class("mono"), g.Text(fieldSummary(p.Fields)))),
			g.If(p.Recipient != nil && len(p.Merged.Missing) > 0, h.P(h.Class("warn"),
				g.Textf("This recipient has no value for %s.", fieldSummary(p.Merged.Missing)),
				g.If(p.Strict, g.Text(" Their letter will be held as failed.")))),
			g.Map(p.Gaps, func(gap FieldGap) g.Node { return gapWarning(gap, p.List.Name, p.Strict) }),
		),
	}
}

func gapWarning(gap FieldGap, listName string, strict bool) g.Node {
	msg := fmt.Sprintf("%d of %d recipients on %s have no value for {{%s}}.", gap.Missing, gap.Total, listName, gap.Field)
	if gap.Missing == gap.Total {
		msg = fmt.Sprintf("%s has no {{%s}} values at all.", listName, gap.Field)
	}
	if strict {
		msg += " Those letters will be held as failed."
	} else {
		msg += " It will render empty for them."
	}
	return h.P(h.Class("warn"), g.Text(msg))
}

func recipientLabel(r *Recipient) string {
	if r.Name != "" {
		return r.Name + " <" + r.Email + ">"
	}
	return r.Email
}

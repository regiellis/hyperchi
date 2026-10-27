package main

import (
	gc "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

func loginView(in LoginForm, fields map[string][]string, csrf string) g.Node {
	return h.Section(h.Class("panel narrow"),
		h.H1(g.Text("Sign in")),
		h.P(h.Class("muted"), g.Text("Mailgrid is for the team. Ask a teammate for an account.")),
		loginForm(in, fields, csrf),
	)
}

// loginForm posts with htmx and swaps itself on a 422, so a wrong password
// keeps the email the person typed.
func loginForm(in LoginForm, fields map[string][]string, csrf string) g.Node {
	return h.Form(h.ID("login-form"), h.Method("post"), h.Action("/login"),
		gc.HxPost("/login"), gc.HxTarget("this"), gc.HxSwap("outerHTML"),
		csrfField(csrf),
		h.Input(h.Type("hidden"), h.Name("next"), h.Value(in.Next)),
		field("login-email", "Email", textInput("login-email", "email", "email", in.Email, fields["email"],
			h.AutoComplete("username"), h.Required(), h.AutoFocus()), fields["email"], ""),
		field("login-password", "Password", textInput("login-password", "password", "password", "", fields["password"],
			h.AutoComplete("current-password"), h.Required()), fields["password"], ""),
		h.Button(h.Type("submit"), h.Class("btn"), g.Text("Sign in")),
	)
}

func setupView(in UserForm, fields map[string][]string, csrf string) g.Node {
	return h.Section(h.Class("panel narrow"),
		h.H1(g.Text("Set up Mailgrid")),
		h.P(g.Text("There are no accounts yet. Create the first one; you can add teammates after.")),
		userForm("/setup", in, fields, csrf, "Create account"),
	)
}

// userForm creates an account, on the setup page or the team page.
func userForm(action string, in UserForm, fields map[string][]string, csrf, button string) g.Node {
	return h.Form(h.ID("user-form"), h.Method("post"), h.Action(action),
		gc.HxPost(action), gc.HxTarget("this"), gc.HxSwap("outerHTML"),
		csrfField(csrf),
		field("user-name", "Name", textInput("user-name", "name", "text", in.Name, fields["name"],
			h.AutoComplete("name"), h.Required(), h.MaxLength("120")), fields["name"], ""),
		field("user-email", "Email", textInput("user-email", "email", "email", in.Email, fields["email"],
			h.AutoComplete("email"), h.Required()), fields["email"], ""),
		field("user-password", "Password", textInput("user-password", "password", "password", "", fields["password"],
			h.AutoComplete("new-password"), h.Required(), h.MinLength("12"), h.MaxLength("72"),
			h.Aria("describedby", "user-password-hint")), fields["password"],
			"At least 12 characters. Stored as a bcrypt hash."),
		h.Button(h.Type("submit"), h.Class("btn"), g.Text(button)),
	)
}

func teamView(users []User, in UserForm, fields map[string][]string, csrf string) g.Node {
	return g.Group{
		pageHeader("Team"),
		h.Div(h.Class("grid-2"),
			h.Div(h.Class("table-wrap"),
				h.Table(h.Class("data"),
					h.Caption(g.Text(plural(len(users), "account", "accounts"))),
					h.THead(h.Tr(h.Th(h.Scope("col"), g.Text("Name")), h.Th(h.Scope("col"), g.Text("Email")),
						h.Th(h.Scope("col"), g.Text("Added")))),
					h.TBody(g.Map(users, func(u User) g.Node {
						return h.Tr(h.Td(g.Text(u.Name)), h.Td(h.Class("mono"), g.Text(u.Email)), h.Td(when(u.CreatedAt)))
					})),
				),
			),
			h.Section(h.Class("panel"), h.Aria("labelledby", "add-teammate"),
				h.H2(h.ID("add-teammate"), g.Text("Add a teammate")),
				h.P(h.Class("muted"), g.Text("Everyone on the team can import lists, write letters, and run send-outs. The audit log records who did what.")),
				userForm("/team", in, fields, csrf, "Add teammate"),
			),
		),
	}
}

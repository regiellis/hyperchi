package main

import "embed"

// assets holds the templates, the CSS, and the vendored scripts (htmx 4.0.0
// and the HyperChi directives runtime), compiled into the binary so the app
// runs from any working directory. site.go loads the templates with
// app.LoadTemplatesFS and serves static/ with app.StaticFS.
//
//go:embed templates static
var assets embed.FS

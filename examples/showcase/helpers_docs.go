package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"showcase/internal/apiref"
)

// The helpers reference is generated from the framework source, because the
// deployed binary carries no source to read. TestHelpersReferenceIsCurrent
// fails when the generated file falls behind the code.
//
//go:generate go run ./internal/apiref/gen
//go:embed reference/helpers.json
var helpersReferenceJSON []byte

func helpersReference() ([]apiref.Group, error) {
	var groups []apiref.Group
	if err := json.Unmarshal(helpersReferenceJSON, &groups); err != nil {
		return nil, fmt.Errorf("reference/helpers.json: %w", err)
	}
	return groups, nil
}

var helpersReferenceTemplate = template.Must(template.New("helpers-reference").Parse(`{{range .}}
<details class="filter-reference helper-reference"><summary>{{.Name}} <span>{{len .Entries}} entries</span></summary>
<p class="helper-import"><code>import "{{.Import}}"</code></p><dl>
{{range .Entries}}<div id="helper-{{.Name}}"><dt><code>{{.Name}}</code></dt><dd><code>{{.Signature}}</code>{{with .Summary}}<p>{{.}}</p>{{end}}</dd></div>{{end}}
</dl></details>
{{end}}`))

// helpersReferenceSlot renders the generated reference for the page, the
// Markdown endpoints, and search.
func helpersReferenceSlot() (docSlot, error) {
	groups, err := helpersReference()
	if err != nil {
		return docSlot{}, err
	}
	var page bytes.Buffer
	if err := helpersReferenceTemplate.Execute(&page, groups); err != nil {
		return docSlot{}, err
	}
	var md, text strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&md, "### %s (%d entries)\n\n`import %q`\n\n", g.Name, len(g.Entries), g.Import)
		fmt.Fprintf(&text, "%s\n", g.Name)
		for _, e := range g.Entries {
			fmt.Fprintf(&md, "- `%s`: `%s`", e.Name, e.Signature)
			if e.Summary != "" {
				fmt.Fprintf(&md, " %s", e.Summary)
			}
			md.WriteString("\n")
			fmt.Fprintf(&text, "%s %s %s\n", e.Name, e.Signature, e.Summary)
		}
		md.WriteString("\n")
	}
	return docSlot{HTML: template.HTML(page.String()), Markdown: md.String(), Text: text.String()}, nil
}

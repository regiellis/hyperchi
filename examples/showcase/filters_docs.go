package main

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"reflect"
	"sort"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi/filters"
)

type filterEntry struct{ Name, Signature string }
type filterGroup struct {
	Name, Names string
	Entries     []filterEntry
}

// filterReference reads signatures from the actual registry so the docs show the
// functions installed by this checkout, including overrides such as contains.
func filterReference() []filterGroup {
	groups := []filterGroup{
		{Name: "Strings", Names: "truncate truncateWords upper lower title capitalize trim trimPrefix trimSuffix replace replaceAll hasPrefix hasSuffix split join repeat slug camelCase snakeCase kebabCase pascalCase pluralize singularize wordWrap indent dedent quote unquote stripTags nl2br br2nl"},
		{Name: "HTML", Names: "safe escape unescape attr css js url urlquery link mailto tel img script style meta classNames addClass removeClass toggleClass dataAttrs ariaAttrs inputAttrs formField select checkbox radio textarea"},
		{Name: "JSON", Names: "toJSON fromJSON prettyJSON compactJSON jsonEscape jsonAttr jsonLD jsonGet jsonSet jsonMerge jsonArray jsonObject"},
		{Name: "Dates and times", Names: "date dateFormat timeAgo timeFrom dateAdd dateSub dateDiff dateCompare now today tomorrow yesterday weekday month year day hour minute second unix fromUnix iso8601 rfc3339 humanDuration durationSeconds timezone"},
		{Name: "Numbers", Names: "number decimal currency percent ordinal bytes round ceil floor abs min max sum avg add sub mul div mod pow sqrt humanize commas padNumber roman"},
		{Name: "Collections", Names: "len first last index slice reverse sort sortBy filter map reduce find findIndex contains unique group chunk flatten zip keys values pairs merge random shuffle take skip pluck"},
		{Name: "HTMX", Names: "hxGet hxPost hxPut hxPatch hxDelete hxTrigger hxTarget hxSwap hxIndicator hxPushURL hxConfirm hxIgnore hxBoost hxVals hxHeaders hxInclude hxSelect hxSelectOOB hxSwapOOB hxValidate hxSync hxOn hxEncoding hxDisabledElt hxPreserve hxReplaceURL hxStatus hxAction hxMethod hxConfig hxOptimistic hxPreload htmxAttrs htmxButton htmxForm htmxLink htmxModal htmxToast htmxSpinner"},
		{Name: "Utilities", Names: "default fallback ternary coalesce empty notEmpty isNil notNil typeOf kindOf eq ne lt le gt ge and or not base64Encode base64Decode urlEncode urlDecode pathJoin pathBase pathDir pathExt regexMatch regexReplace regexSplit uuid env dict list printf print println"},
	}
	registry := filters.FuncMap()
	for i := range groups {
		for _, name := range strings.Fields(groups[i].Names) {
			groups[i].Entries = append(groups[i].Entries, filterEntry{name, reflect.TypeOf(registry[name]).String()})
		}
		sort.Slice(groups[i].Entries, func(a, b int) bool { return groups[i].Entries[a].Name < groups[i].Entries[b].Name })
	}
	return groups
}

// filterRecipe is one "Recipes with live output" example. The result is
// computed from the installed filters at startup, so the page shows what the
// expression really prints.
type filterRecipe struct{ Title, Source string }

var filterRecipes = []filterRecipe{
	{"Clean a label", `{{ "  field notebook  " | trim | title }}`},
	{"Shorten text", `{{ truncate "A notebook for every journey" 18 }}`},
	{"Format a price", `{{ currency 1234.5 "$" }}`},
	{"Display a proportion", `{{ percent 0.125 1 }}`},
	{"Human-readable storage", `{{ bytes 1536 }}`},
	{"Format an ISO date", `{{ "2026-10-05T12:00:00Z" | dateFormat "Jan 2, 2006" }}`},
	{"Use a fallback", `{{ default "" "Anonymous" }}`},
	{"Sort a collection", `{{ range sort (list "htmx" "go" "html") }}{{ . }} {{ end }}`},
	{"Read nested JSON", "{{ jsonGet (fromJSON `{\"user\":{\"name\":\"Morgan\"}}`) \"user.name\" }}"},
	{"Count items", `{{ len (list "Go" "HTML" "HTMX") }}`},
}

// recipeSlot renders every recipe through html/template with the registered
// filters, exactly as an application template would.
func recipeSlot() (docSlot, error) {
	var page, md, text strings.Builder
	for _, r := range filterRecipes {
		t, err := template.New(r.Title).Funcs(filters.FuncMap()).Parse(r.Source)
		if err != nil {
			return docSlot{}, fmt.Errorf("recipe %q: %w", r.Title, err)
		}
		var out bytes.Buffer
		if err := t.Execute(&out, nil); err != nil {
			return docSlot{}, fmt.Errorf("recipe %q: %w", r.Title, err)
		}
		// out is html/template output, already escaped for element content.
		fmt.Fprintf(&page, `<h3>%s</h3><pre><code>%s</code></pre><div class="docs-filter-output"><span>Result</span><output>%s</output></div>`+"\n",
			html.EscapeString(r.Title), html.EscapeString(r.Source), out.String())
		result := strings.TrimSpace(html.UnescapeString(out.String()))
		fmt.Fprintf(&md, "### %s\n\n```gotemplate\n%s\n```\n\nResult: `%s`\n\n", r.Title, r.Source, result)
		fmt.Fprintf(&text, "%s\n%s\nResult: %s\n", r.Title, r.Source, result)
	}
	return docSlot{HTML: template.HTML(page.String()), Markdown: md.String(), Text: text.String()}, nil
}

var referenceTemplate = template.Must(template.New("reference").Parse(`{{range .}}
<details class="filter-reference"><summary>{{.Name}} <span>{{len .Entries}} functions</span></summary><dl>
{{range .Entries}}<div id="filter-{{.Name}}"><dt><code>{{.Name}}</code></dt><dd><code>{{.Signature}}</code></dd></div>{{end}}
</dl></details>
{{end}}`))

// referenceSlot renders the complete registry reference for the page, the
// Markdown endpoints, and search.
func referenceSlot() (docSlot, error) {
	groups := filterReference()
	var page bytes.Buffer
	if err := referenceTemplate.Execute(&page, groups); err != nil {
		return docSlot{}, err
	}
	var md, text strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&md, "### %s (%d functions)\n\n", g.Name, len(g.Entries))
		fmt.Fprintf(&text, "%s\n", g.Name)
		for _, e := range g.Entries {
			fmt.Fprintf(&md, "- `%s`: `%s`\n", e.Name, e.Signature)
			fmt.Fprintf(&text, "%s %s\n", e.Name, e.Signature)
		}
		md.WriteString("\n")
	}
	return docSlot{HTML: template.HTML(page.String()), Markdown: md.String(), Text: text.String()}, nil
}

// directivesDemo is the live counter on the directives page. It runs in the
// page's site() scope and has no Markdown form.
const directivesDemo = `<div class="docs-demo"><button class="btn btn-primary" hc-on:click="count++">Add one +</button><output hc-text="count" aria-live="polite">0</output><button class="btn btn-ghost" hc-on:click="count = 0">Reset</button></div>` + "\n"

// docSlots returns every generated block the Markdown files may place.
func docSlots() (map[string]docSlot, error) {
	recipes, err := recipeSlot()
	if err != nil {
		return nil, err
	}
	reference, err := referenceSlot()
	if err != nil {
		return nil, err
	}
	helpersRef, err := helpersReferenceSlot()
	if err != nil {
		return nil, err
	}
	return map[string]docSlot{
		"filter-recipes":    recipes,
		"filter-reference":  reference,
		"helpers-reference": helpersRef,
		"directives-demo":   {HTML: directivesDemo},
	}, nil
}

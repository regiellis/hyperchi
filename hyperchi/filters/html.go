package filters

import (
	"fmt"
	"html"
	"html/template"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

func registerHTMLFilters() {
	Register("safe", safe)
	Register("escape", escape)
	Register("unescape", unescape)
	Register("attr", attr)
	Register("css", css)
	Register("js", js)
	Register("url", urlEscape)
	Register("urlquery", url.QueryEscape)
	Register("link", link)
	Register("mailto", mailto)
	Register("tel", tel)
	Register("img", img)
	Register("script", script)
	Register("style", style)
	Register("meta", meta)
	Register("classNames", classNames)
	Register("addClass", addClass)
	Register("removeClass", removeClass)
	Register("toggleClass", toggleClass)
	Register("dataAttrs", dataAttrs)
	Register("ariaAttrs", ariaAttrs)
	Register("inputAttrs", inputAttrs)
	Register("formField", formField)
	Register("select", selectField)
	Register("checkbox", checkbox)
	Register("radio", radio)
	Register("textarea", textarea)
}

func safe(s string) template.HTML {
	return template.HTML(s)
}

func escape(s string) string {
	return html.EscapeString(s)
}

func unescape(s string) template.HTML {
	return template.HTML(html.UnescapeString(s))
}

// attr writes name="value" with the value HTML-escaped, or nothing when value
// is empty. A name that is not a safe attribute name returns an error.
func attr(name, value string) (template.HTMLAttr, error) {
	if err := checkAttrName("attr", name, name); err != nil {
		return "", err
	}
	if value == "" {
		return "", nil
	}
	return template.HTMLAttr(fmt.Sprintf(`%s="%s"`, name, html.EscapeString(value))), nil
}

func css(s string) template.CSS {
	return template.CSS(s)
}

func js(s string) template.JS {
	return template.JS(s)
}

func urlEscape(s string) string {
	return url.PathEscape(s)
}

// unsafeURLPlaceholder replaces URLs whose scheme is not allowlisted, mirroring
// html/template's ZgotmplZ convention: the failure is visible in output but inert.
const unsafeURLPlaceholder = "#ZgotmplZ"

// safeURL returns raw only when it is relative or uses an allowlisted scheme
// (http, https, mailto, tel). Anything else (javascript:, data:, vbscript:,
// unparseable input) is replaced with an inert placeholder. These helpers
// return template.HTML, so the auto-escaper never re-checks the URL; the
// scheme must be validated here.
func safeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return unsafeURLPlaceholder
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https", "mailto", "tel":
		return raw
	}
	return unsafeURLPlaceholder
}

func link(text, href string, attrs ...template.HTMLAttr) template.HTML {
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}
	return template.HTML(fmt.Sprintf(`<a href="%s"%s>%s</a>`,
		html.EscapeString(safeURL(href)), attrStr, html.EscapeString(text)))
}

func mailto(email string, text ...string) template.HTML {
	displayText := email
	if len(text) > 0 {
		displayText = text[0]
	}
	return template.HTML(fmt.Sprintf(`<a href="mailto:%s">%s</a>`,
		html.EscapeString(email), html.EscapeString(displayText)))
}

func tel(phone string, text ...string) template.HTML {
	displayText := phone
	if len(text) > 0 {
		displayText = text[0]
	}
	cleanPhone := strings.ReplaceAll(phone, " ", "")
	cleanPhone = strings.ReplaceAll(cleanPhone, "-", "")
	cleanPhone = strings.ReplaceAll(cleanPhone, "(", "")
	cleanPhone = strings.ReplaceAll(cleanPhone, ")", "")
	return template.HTML(fmt.Sprintf(`<a href="tel:%s">%s</a>`,
		html.EscapeString(cleanPhone), html.EscapeString(displayText)))
}

func img(src string, alt string, attrs ...template.HTMLAttr) template.HTML {
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}
	return template.HTML(fmt.Sprintf(`<img src="%s" alt="%s"%s>`,
		html.EscapeString(safeURL(src)), html.EscapeString(alt), attrStr))
}

func script(src string, attrs ...template.HTMLAttr) template.HTML {
	if src == "" {
		return ""
	}
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}
	return template.HTML(fmt.Sprintf(`<script src="%s"%s></script>`,
		html.EscapeString(safeURL(src)), attrStr))
}

func style(href string, attrs ...template.HTMLAttr) template.HTML {
	if href == "" {
		return ""
	}
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}
	return template.HTML(fmt.Sprintf(`<link rel="stylesheet" href="%s"%s>`,
		html.EscapeString(safeURL(href)), attrStr))
}

func meta(name, content string) template.HTML {
	return template.HTML(fmt.Sprintf(`<meta name="%s" content="%s">`,
		html.EscapeString(name), html.EscapeString(content)))
}

func classNames(classes ...any) string {
	var result []string

	for _, class := range classes {
		switch v := class.(type) {
		case string:
			if v != "" {
				result = append(result, v)
			}
		case []string:
			for _, c := range v {
				if c != "" {
					result = append(result, c)
				}
			}
		case map[string]bool:
			for c, enabled := range v {
				if enabled && c != "" {
					result = append(result, c)
				}
			}
		case bool:
			// A bare bool names no class; use a map[string]bool to toggle one.
		default:
			if s := fmt.Sprint(v); s != "" {
				result = append(result, s)
			}
		}
	}

	return strings.Join(result, " ")
}

func addClass(existing, new string) string {
	if existing == "" {
		return new
	}
	if new == "" {
		return existing
	}
	classes := strings.Fields(existing)
	for _, newClass := range strings.Fields(new) {
		found := false
		for _, c := range classes {
			if c == newClass {
				found = true
				break
			}
		}
		if !found {
			classes = append(classes, newClass)
		}
	}
	return strings.Join(classes, " ")
}

func removeClass(existing, remove string) string {
	if existing == "" || remove == "" {
		return existing
	}

	removeMap := make(map[string]bool)
	for _, r := range strings.Fields(remove) {
		removeMap[r] = true
	}

	var result []string
	for _, c := range strings.Fields(existing) {
		if !removeMap[c] {
			result = append(result, c)
		}
	}

	return strings.Join(result, " ")
}

func toggleClass(existing, toggle string) string {
	classes := strings.Fields(existing)
	toggleClasses := strings.Fields(toggle)

	classMap := make(map[string]bool)
	for _, c := range classes {
		classMap[c] = true
	}

	for _, t := range toggleClasses {
		if classMap[t] {
			delete(classMap, t)
		} else {
			classMap[t] = true
		}
	}

	var result []string
	for c := range classMap {
		result = append(result, c)
	}

	return strings.Join(result, " ")
}

// attrNameRe matches an attribute name that is safe to write as is: a
// letter or underscore, then letters, digits, and _ . : -. Filter input that
// becomes an attribute name cannot be escaped, so anything else is refused.
var attrNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]*$`)

// checkAttrName returns an error naming filter and the caller's input when
// name is not a safe attribute name (see attrNameRe).
func checkAttrName(filter, input, name string) error {
	if !attrNameRe.MatchString(name) {
		return fmt.Errorf("%s: %q is not a safe attribute name", filter, input)
	}
	return nil
}

// namedAttrs returns prefix+key="value" for each entry of attrs, sorted by
// name, with values HTML-escaped. A key that does not make a safe attribute
// name returns an error naming filter.
func namedAttrs(filter, prefix string, attrs map[string]string) ([]string, error) {
	parts := make([]string, 0, len(attrs))
	for _, key := range slices.Sorted(maps.Keys(attrs)) {
		if key == "" {
			return nil, fmt.Errorf("%s: an empty name is not a safe attribute name", filter)
		}
		if err := checkAttrName(filter, key, prefix+key); err != nil {
			return nil, err
		}
		parts = append(parts, fmt.Sprintf(`%s%s="%s"`, prefix, key, html.EscapeString(attrs[key])))
	}
	return parts, nil
}

// dataAttrs writes a data-* attribute per entry, adding the data- prefix
// when the key lacks it, sorted by name, with values HTML-escaped. A key that
// does not make a safe attribute name returns an error.
func dataAttrs(attrs map[string]any) (template.HTMLAttr, error) {
	return prefixedAttrs("dataAttrs", "data-", attrs)
}

// ariaAttrs writes an aria-* attribute per entry, by the rules of dataAttrs.
func ariaAttrs(attrs map[string]any) (template.HTMLAttr, error) {
	return prefixedAttrs("ariaAttrs", "aria-", attrs)
}

func prefixedAttrs(filter, prefix string, attrs map[string]any) (template.HTMLAttr, error) {
	names := make(map[string]any, len(attrs))
	for k, v := range attrs {
		name := k
		if !strings.HasPrefix(name, prefix) {
			name = prefix + name
		}
		if name == prefix {
			return "", fmt.Errorf("%s: %q is not a safe attribute name", filter, k)
		}
		if err := checkAttrName(filter, k, name); err != nil {
			return "", err
		}
		names[name] = v
	}
	parts := make([]string, 0, len(names))
	for _, name := range slices.Sorted(maps.Keys(names)) {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, name, html.EscapeString(fmt.Sprint(names[name]))))
	}
	return template.HTMLAttr(strings.Join(parts, " ")), nil
}

// InputOptions configures the inputAttrs filter. Empty strings and false
// flags are left out of the output; Value is written when non-nil. Every
// value is HTML-escaped. Attrs keys become attribute names, written in sorted
// order; a key that is not a safe attribute name makes inputAttrs return an
// error.
type InputOptions struct {
	Type        string
	Name        string
	Value       any
	ID          string
	Class       string
	Placeholder string
	Required    bool
	Disabled    bool
	Readonly    bool
	Attrs       map[string]string
}

func inputAttrs(opts InputOptions) (template.HTMLAttr, error) {
	var parts []string

	if opts.Type != "" {
		parts = append(parts, fmt.Sprintf(`type="%s"`, html.EscapeString(opts.Type)))
	}
	if opts.Name != "" {
		parts = append(parts, fmt.Sprintf(`name="%s"`, html.EscapeString(opts.Name)))
	}
	if opts.ID != "" {
		parts = append(parts, fmt.Sprintf(`id="%s"`, html.EscapeString(opts.ID)))
	}
	if opts.Class != "" {
		parts = append(parts, fmt.Sprintf(`class="%s"`, html.EscapeString(opts.Class)))
	}
	if opts.Value != nil {
		parts = append(parts, fmt.Sprintf(`value="%s"`, html.EscapeString(fmt.Sprint(opts.Value))))
	}
	if opts.Placeholder != "" {
		parts = append(parts, fmt.Sprintf(`placeholder="%s"`, html.EscapeString(opts.Placeholder)))
	}
	if opts.Required {
		parts = append(parts, "required")
	}
	if opts.Disabled {
		parts = append(parts, "disabled")
	}
	if opts.Readonly {
		parts = append(parts, "readonly")
	}

	extra, err := namedAttrs("inputAttrs", "", opts.Attrs)
	if err != nil {
		return "", err
	}
	parts = append(parts, extra...)

	return template.HTMLAttr(strings.Join(parts, " ")), nil
}

func formField(fieldType, name string, value any, attrs ...template.HTMLAttr) template.HTML {
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}

	return template.HTML(fmt.Sprintf(`<input type="%s" name="%s" value="%s"%s>`,
		html.EscapeString(fieldType),
		html.EscapeString(name),
		html.EscapeString(fmt.Sprint(value)),
		attrStr))
}

// SelectOption is one <option> of the selectField filter.
type SelectOption struct {
	Value    string
	Text     string
	Selected bool
}

func selectField(name string, options []SelectOption, attrs ...template.HTMLAttr) template.HTML {
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}

	var optionHTML []string
	for _, opt := range options {
		selected := ""
		if opt.Selected {
			selected = " selected"
		}
		optionHTML = append(optionHTML, fmt.Sprintf(`<option value="%s"%s>%s</option>`,
			html.EscapeString(opt.Value),
			selected,
			html.EscapeString(opt.Text)))
	}

	return template.HTML(fmt.Sprintf(`<select name="%s"%s>%s</select>`,
		html.EscapeString(name),
		attrStr,
		strings.Join(optionHTML, "")))
}

func checkbox(name string, value any, checked bool, attrs ...template.HTMLAttr) template.HTML {
	checkedAttr := ""
	if checked {
		checkedAttr = " checked"
	}

	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}

	return template.HTML(fmt.Sprintf(`<input type="checkbox" name="%s" value="%s"%s%s>`,
		html.EscapeString(name),
		html.EscapeString(fmt.Sprint(value)),
		checkedAttr,
		attrStr))
}

func radio(name string, value any, checked bool, attrs ...template.HTMLAttr) template.HTML {
	checkedAttr := ""
	if checked {
		checkedAttr = " checked"
	}

	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}

	return template.HTML(fmt.Sprintf(`<input type="radio" name="%s" value="%s"%s%s>`,
		html.EscapeString(name),
		html.EscapeString(fmt.Sprint(value)),
		checkedAttr,
		attrStr))
}

func textarea(name string, value any, rows, cols int, attrs ...template.HTMLAttr) template.HTML {
	attrStr := joinAttrs(attrs)
	if attrStr != "" {
		attrStr = " " + attrStr
	}

	rowColAttr := ""
	if rows > 0 {
		rowColAttr += fmt.Sprintf(` rows="%d"`, rows)
	}
	if cols > 0 {
		rowColAttr += fmt.Sprintf(` cols="%d"`, cols)
	}

	return template.HTML(fmt.Sprintf(`<textarea name="%s"%s%s>%s</textarea>`,
		html.EscapeString(name),
		rowColAttr,
		attrStr,
		html.EscapeString(fmt.Sprint(value))))
}

// joinAttrs joins the trailing attributes of an element filter. They are typed
// template.HTMLAttr so only attributes the template author wrote (literals) or
// that Go code marked trusted reach the markup; a plain string from data fails
// the render instead.
func joinAttrs(attrs []template.HTMLAttr) string {
	parts := make([]string, len(attrs))
	for i, a := range attrs {
		parts[i] = string(a)
	}
	return strings.Join(parts, " ")
}

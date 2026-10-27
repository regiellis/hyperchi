package filters

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strings"
)

func registerHTMXFilters() {
	Register("hxGet", hxGet)
	Register("hxPost", hxPost)
	Register("hxPut", hxPut)
	Register("hxPatch", hxPatch)
	Register("hxDelete", hxDelete)

	Register("hxTrigger", hxTrigger)
	Register("hxTarget", hxTarget)
	Register("hxSwap", hxSwap)
	Register("hxIndicator", hxIndicator)
	Register("hxPushURL", hxPushURL)
	Register("hxConfirm", hxConfirm)
	Register("hxIgnore", hxIgnore)
	Register("hxBoost", hxBoost)
	Register("hxVals", hxVals)
	Register("hxHeaders", hxHeaders)
	Register("hxInclude", hxInclude)
	Register("hxSelect", hxSelect)
	Register("hxSelectOOB", hxSelectOOB)
	Register("hxSwapOOB", hxSwapOOB)
	Register("hxValidate", hxValidate)
	Register("hxSync", hxSync)
	Register("hxOn", hxOn)

	Register("hxEncoding", hxEncoding)
	Register("hxDisabledElt", hxDisabledElt)
	Register("hxPreserve", hxPreserve)
	Register("hxReplaceURL", hxReplaceURL)
	Register("hxStatus", hxStatus)

	// Attributes htmx 4 introduced.
	Register("hxAction", hxAction)
	Register("hxMethod", hxMethod)
	Register("hxConfig", hxConfig)
	Register("hxOptimistic", hxOptimistic)
	Register("hxPreload", hxPreload)

	// Builders that write several attributes or a whole element.
	Register("htmxAttrs", htmxAttrs)
	Register("htmxButton", htmxButton)
	Register("htmxForm", htmxForm)
	Register("htmxLink", htmxLink)
	Register("htmxModal", htmxModal)
	Register("htmxToast", htmxToast)
	Register("htmxSpinner", htmxSpinner)
}

func hxGet(url string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-get="%s"`, html.EscapeString(url)))
}

func hxPost(url string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-post="%s"`, html.EscapeString(url)))
}

func hxPut(url string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-put="%s"`, html.EscapeString(url)))
}

func hxPatch(url string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-patch="%s"`, html.EscapeString(url)))
}

func hxDelete(url string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-delete="%s"`, html.EscapeString(url)))
}

func hxTrigger(trigger string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-trigger="%s"`, html.EscapeString(trigger)))
}

func hxTarget(target string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-target="%s"`, html.EscapeString(target)))
}

func hxSwap(swap string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-swap="%s"`, html.EscapeString(swap)))
}

func hxIndicator(indicator string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-indicator="%s"`, html.EscapeString(indicator)))
}

func hxPushURL(url string) template.HTMLAttr {
	if url == "true" || url == "false" {
		return template.HTMLAttr(fmt.Sprintf(`hx-push-url="%s"`, url))
	}
	return template.HTMLAttr(fmt.Sprintf(`hx-push-url="%s"`, html.EscapeString(url)))
}

func hxConfirm(message string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-confirm="%s"`, html.EscapeString(message)))
}

func hxIgnore() template.HTMLAttr {
	return template.HTMLAttr(`hx-ignore`)
}

func hxBoost(enabled bool) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-boost="%t"`, enabled))
}

// hxVals writes hx-vals with values JSON-encoded and HTML-escaped. An empty
// or unencodable map writes nothing.
func hxVals(values map[string]any) template.HTMLAttr {
	if len(values) == 0 {
		return ""
	}
	return jsonAttribute("hx-vals", values)
}

// hxHeaders writes hx-headers with headers JSON-encoded and HTML-escaped. An
// empty map writes nothing.
func hxHeaders(headers map[string]string) template.HTMLAttr {
	if len(headers) == 0 {
		return ""
	}
	return jsonAttribute("hx-headers", headers)
}

// jsonAttribute writes name="<JSON>" with the JSON HTML-escaped, so no value
// can end the attribute; the browser unescapes it before htmx parses the JSON.
// A value json.Marshal rejects writes nothing.
func jsonAttribute(name string, value any) template.HTMLAttr {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return template.HTMLAttr(name + `="` + html.EscapeString(string(body)) + `"`)
}

func hxInclude(selector string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-include="%s"`, html.EscapeString(selector)))
}

func hxSelect(selector string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-select="%s"`, html.EscapeString(selector)))
}

func hxSelectOOB(selector string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-select-oob="%s"`, html.EscapeString(selector)))
}

func hxSwapOOB(swap string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-swap-oob="%s"`, html.EscapeString(swap)))
}

func hxValidate(validate bool) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-validate="%t"`, validate))
}

func hxSync(sync string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-sync="%s"`, html.EscapeString(sync)))
}

// hxOn writes hx-on:event="action" with the action HTML-escaped. The event
// becomes part of the attribute name, so one that does not make a safe
// attribute name (letters, digits, and _ . : -) returns an error.
func hxOn(event, action string) (template.HTMLAttr, error) {
	parts, err := namedAttrs("hxOn", "hx-on:", map[string]string{event: action})
	if err != nil {
		return "", err
	}
	return template.HTMLAttr(parts[0]), nil
}

// HTMXAttrs configures the htmxAttrs filter, which writes one hx-*
// attribute for each non-zero field. String values are HTML-escaped; Vals,
// Headers, and Config are JSON-encoded, then HTML-escaped, and an
// unencodable map is left out. On keys are event names written as
// hx-on:<event> in sorted order; one that does not make a safe attribute name
// makes htmxAttrs return an error.
type HTMXAttrs struct {
	// Request URLs, one per HTTP method.
	Get    string
	Post   string
	Put    string
	Patch  string
	Delete string

	Trigger   string
	Target    string
	Swap      string
	Indicator string
	PushURL   string
	Confirm   string
	Boost     bool
	Vals      map[string]any
	Headers   map[string]string
	Include   string
	Select    string
	SelectOOB string
	SwapOOB   string
	Validate  bool
	Sync      string
	On        map[string]string

	Encoding    string
	DisabledElt string // written as hx-disable, the htmx 4 name
	Preserve    any    // bool or string
	ReplaceURL  any    // bool or string

	// htmx 4 attributes.
	Action     string         // hx-action: request URL, used with Method
	Method     string         // hx-method: HTTP method, used with Action
	Config     map[string]any // hx-config: per-element request config, JSON-encoded
	Optimistic string         // hx-optimistic: content shown before the server responds
	Preload    string         // hx-preload: event that starts the request early, such as "mouseover"
}

func htmxAttrs(attrs HTMXAttrs) (template.HTMLAttr, error) {
	var parts []string

	if attrs.Get != "" {
		parts = append(parts, fmt.Sprintf(`hx-get="%s"`, html.EscapeString(attrs.Get)))
	}
	if attrs.Post != "" {
		parts = append(parts, fmt.Sprintf(`hx-post="%s"`, html.EscapeString(attrs.Post)))
	}
	if attrs.Put != "" {
		parts = append(parts, fmt.Sprintf(`hx-put="%s"`, html.EscapeString(attrs.Put)))
	}
	if attrs.Patch != "" {
		parts = append(parts, fmt.Sprintf(`hx-patch="%s"`, html.EscapeString(attrs.Patch)))
	}
	if attrs.Delete != "" {
		parts = append(parts, fmt.Sprintf(`hx-delete="%s"`, html.EscapeString(attrs.Delete)))
	}
	if attrs.Trigger != "" {
		parts = append(parts, fmt.Sprintf(`hx-trigger="%s"`, html.EscapeString(attrs.Trigger)))
	}
	if attrs.Target != "" {
		parts = append(parts, fmt.Sprintf(`hx-target="%s"`, html.EscapeString(attrs.Target)))
	}
	if attrs.Swap != "" {
		parts = append(parts, fmt.Sprintf(`hx-swap="%s"`, html.EscapeString(attrs.Swap)))
	}
	if attrs.Indicator != "" {
		parts = append(parts, fmt.Sprintf(`hx-indicator="%s"`, html.EscapeString(attrs.Indicator)))
	}
	if attrs.PushURL != "" {
		parts = append(parts, fmt.Sprintf(`hx-push-url="%s"`, html.EscapeString(attrs.PushURL)))
	}
	if attrs.Confirm != "" {
		parts = append(parts, fmt.Sprintf(`hx-confirm="%s"`, html.EscapeString(attrs.Confirm)))
	}
	if attrs.Boost {
		parts = append(parts, `hx-boost="true"`)
	}
	if attr := hxVals(attrs.Vals); attr != "" {
		parts = append(parts, string(attr))
	}
	if attr := hxHeaders(attrs.Headers); attr != "" {
		parts = append(parts, string(attr))
	}
	if attrs.Include != "" {
		parts = append(parts, fmt.Sprintf(`hx-include="%s"`, html.EscapeString(attrs.Include)))
	}
	if attrs.Select != "" {
		parts = append(parts, fmt.Sprintf(`hx-select="%s"`, html.EscapeString(attrs.Select)))
	}
	if attrs.SelectOOB != "" {
		parts = append(parts, fmt.Sprintf(`hx-select-oob="%s"`, html.EscapeString(attrs.SelectOOB)))
	}
	if attrs.SwapOOB != "" {
		parts = append(parts, fmt.Sprintf(`hx-swap-oob="%s"`, html.EscapeString(attrs.SwapOOB)))
	}
	if attrs.Validate {
		parts = append(parts, `hx-validate="true"`)
	}
	if attrs.Sync != "" {
		parts = append(parts, fmt.Sprintf(`hx-sync="%s"`, html.EscapeString(attrs.Sync)))
	}
	on, err := namedAttrs("htmxAttrs", "hx-on:", attrs.On)
	if err != nil {
		return "", err
	}
	parts = append(parts, on...)
	if attrs.Encoding != "" {
		parts = append(parts, fmt.Sprintf(`hx-encoding="%s"`, html.EscapeString(attrs.Encoding)))
	}
	if attrs.DisabledElt != "" {
		parts = append(parts, fmt.Sprintf(`hx-disable="%s"`, html.EscapeString(attrs.DisabledElt)))
	}
	if attrs.Preserve != nil {
		switch v := attrs.Preserve.(type) {
		case bool:
			parts = append(parts, fmt.Sprintf(`hx-preserve="%t"`, v))
		case string:
			parts = append(parts, fmt.Sprintf(`hx-preserve="%s"`, html.EscapeString(v)))
		default:
			parts = append(parts, fmt.Sprintf(`hx-preserve="%v"`, v))
		}
	}
	if attrs.ReplaceURL != nil {
		switch v := attrs.ReplaceURL.(type) {
		case bool:
			parts = append(parts, fmt.Sprintf(`hx-replace-url="%t"`, v))
		case string:
			parts = append(parts, fmt.Sprintf(`hx-replace-url="%s"`, html.EscapeString(v)))
		default:
			parts = append(parts, fmt.Sprintf(`hx-replace-url="%v"`, v))
		}
	}

	if attrs.Action != "" {
		parts = append(parts, fmt.Sprintf(`hx-action="%s"`, html.EscapeString(attrs.Action)))
	}
	if attrs.Method != "" {
		parts = append(parts, fmt.Sprintf(`hx-method="%s"`, html.EscapeString(attrs.Method)))
	}
	if attr := hxConfig(attrs.Config); attr != "" {
		parts = append(parts, string(attr))
	}
	if attrs.Optimistic != "" {
		parts = append(parts, fmt.Sprintf(`hx-optimistic="%s"`, html.EscapeString(attrs.Optimistic)))
	}
	if attrs.Preload != "" {
		parts = append(parts, fmt.Sprintf(`hx-preload="%s"`, html.EscapeString(attrs.Preload)))
	}

	return template.HTMLAttr(strings.Join(parts, " ")), nil
}

// HTMXButtonOptions configures the htmxButton filter. Method defaults to
// get, Swap to innerHTML, and Trigger to click. Every value is HTML-escaped.
// Attrs keys become attribute names, written in sorted order; a key that is
// not a safe attribute name makes htmxButton return an error.
type HTMXButtonOptions struct {
	Text      string
	URL       string
	Method    string
	Target    string
	Swap      string
	Trigger   string
	Confirm   string
	Indicator string
	Class     string
	ID        string
	Attrs     map[string]string
}

func htmxButton(opts HTMXButtonOptions) (template.HTML, error) {
	if opts.Method == "" {
		opts.Method = "get"
	}
	if opts.Swap == "" {
		opts.Swap = "innerHTML"
	}
	if opts.Trigger == "" {
		opts.Trigger = "click"
	}

	var attrs []string

	switch strings.ToLower(opts.Method) {
	case "post":
		attrs = append(attrs, fmt.Sprintf(`hx-post="%s"`, html.EscapeString(opts.URL)))
	case "put":
		attrs = append(attrs, fmt.Sprintf(`hx-put="%s"`, html.EscapeString(opts.URL)))
	case "patch":
		attrs = append(attrs, fmt.Sprintf(`hx-patch="%s"`, html.EscapeString(opts.URL)))
	case "delete":
		attrs = append(attrs, fmt.Sprintf(`hx-delete="%s"`, html.EscapeString(opts.URL)))
	default:
		attrs = append(attrs, fmt.Sprintf(`hx-get="%s"`, html.EscapeString(opts.URL)))
	}

	if opts.Target != "" {
		attrs = append(attrs, fmt.Sprintf(`hx-target="%s"`, html.EscapeString(opts.Target)))
	}
	attrs = append(attrs, fmt.Sprintf(`hx-swap="%s"`, html.EscapeString(opts.Swap)))
	attrs = append(attrs, fmt.Sprintf(`hx-trigger="%s"`, html.EscapeString(opts.Trigger)))

	if opts.Confirm != "" {
		attrs = append(attrs, fmt.Sprintf(`hx-confirm="%s"`, html.EscapeString(opts.Confirm)))
	}
	if opts.Indicator != "" {
		attrs = append(attrs, fmt.Sprintf(`hx-indicator="%s"`, html.EscapeString(opts.Indicator)))
	}
	if opts.Class != "" {
		attrs = append(attrs, fmt.Sprintf(`class="%s"`, html.EscapeString(opts.Class)))
	}
	if opts.ID != "" {
		attrs = append(attrs, fmt.Sprintf(`id="%s"`, html.EscapeString(opts.ID)))
	}

	extra, err := namedAttrs("htmxButton", "", opts.Attrs)
	if err != nil {
		return "", err
	}
	attrs = append(attrs, extra...)

	return template.HTML(fmt.Sprintf(`<button %s>%s</button>`,
		strings.Join(attrs, " "), html.EscapeString(opts.Text))), nil
}

// HTMXFormOptions configures the htmxForm filter. Method defaults to post
// and Swap to innerHTML. Every value is HTML-escaped. Attrs keys become
// attribute names, written in sorted order; a key that is not a safe
// attribute name makes htmxForm return an error.
type HTMXFormOptions struct {
	Action    string
	Method    string
	Target    string
	Swap      string
	Trigger   string
	Indicator string
	Validate  bool
	Class     string
	ID        string
	Attrs     map[string]string
}

func htmxForm(opts HTMXFormOptions) (template.HTML, error) {
	if opts.Method == "" {
		opts.Method = "post"
	}
	if opts.Swap == "" {
		opts.Swap = "innerHTML"
	}

	var attrs []string

	switch strings.ToLower(opts.Method) {
	case "get":
		attrs = append(attrs, fmt.Sprintf(`hx-get="%s"`, html.EscapeString(opts.Action)))
	case "put":
		attrs = append(attrs, fmt.Sprintf(`hx-put="%s"`, html.EscapeString(opts.Action)))
	case "patch":
		attrs = append(attrs, fmt.Sprintf(`hx-patch="%s"`, html.EscapeString(opts.Action)))
	case "delete":
		attrs = append(attrs, fmt.Sprintf(`hx-delete="%s"`, html.EscapeString(opts.Action)))
	default:
		attrs = append(attrs, fmt.Sprintf(`hx-post="%s"`, html.EscapeString(opts.Action)))
	}

	if opts.Target != "" {
		attrs = append(attrs, fmt.Sprintf(`hx-target="%s"`, html.EscapeString(opts.Target)))
	}
	attrs = append(attrs, fmt.Sprintf(`hx-swap="%s"`, html.EscapeString(opts.Swap)))

	if opts.Trigger != "" {
		attrs = append(attrs, fmt.Sprintf(`hx-trigger="%s"`, html.EscapeString(opts.Trigger)))
	}
	if opts.Indicator != "" {
		attrs = append(attrs, fmt.Sprintf(`hx-indicator="%s"`, html.EscapeString(opts.Indicator)))
	}
	if opts.Validate {
		attrs = append(attrs, `hx-validate="true"`)
	}
	if opts.Class != "" {
		attrs = append(attrs, fmt.Sprintf(`class="%s"`, html.EscapeString(opts.Class)))
	}
	if opts.ID != "" {
		attrs = append(attrs, fmt.Sprintf(`id="%s"`, html.EscapeString(opts.ID)))
	}

	extra, err := namedAttrs("htmxForm", "", opts.Attrs)
	if err != nil {
		return "", err
	}
	attrs = append(attrs, extra...)

	return template.HTML(fmt.Sprintf(`<form %s>`, strings.Join(attrs, " "))), nil
}

// htmxLink writes a boosted link: hx-get to url with hx-push-url. In the
// optional map, "target" and "swap" become hx-target and hx-swap, and every
// other key is an attribute name, written in sorted order. A key that is not
// a safe attribute name returns an error.
func htmxLink(text, url string, opts ...map[string]string) (template.HTML, error) {
	attrs := []string{
		fmt.Sprintf(`hx-get="%s"`, html.EscapeString(url)),
		`hx-push-url="true"`,
	}

	if len(opts) > 0 {
		named := make(map[string]string, len(opts[0]))
		for k, v := range opts[0] {
			switch k {
			case "target":
				named["hx-target"] = v
			case "swap":
				named["hx-swap"] = v
			default:
				named[k] = v
			}
		}
		extra, err := namedAttrs("htmxLink", "", named)
		if err != nil {
			return "", err
		}
		attrs = append(attrs, extra...)
	}

	return template.HTML(fmt.Sprintf(`<a href="%s" %s>%s</a>`,
		html.EscapeString(url),
		strings.Join(attrs, " "),
		html.EscapeString(text))), nil
}

func htmxModal(id string, content template.HTML) template.HTML {
	return template.HTML(fmt.Sprintf(`
<div id="%s" class="modal" _="on closeModal remove me">
    <div class="modal-backdrop" _="on click trigger closeModal"></div>
    <div class="modal-content">
        %s
    </div>
</div>`, html.EscapeString(id), content))
}

func htmxToast(message string, toastType string) template.HTML {
	class := "toast"
	if toastType != "" {
		class += " toast-" + toastType
	}

	return template.HTML(fmt.Sprintf(`
<div class="%s" _="on load wait 3s then remove me">
    %s
</div>`, html.EscapeString(class), html.EscapeString(message)))
}

func htmxSpinner(id string) template.HTML {
	return template.HTML(fmt.Sprintf(`
<div id="%s" class="htmx-spinner" style="display:none">
    <div class="spinner"></div>
</div>`, html.EscapeString(id)))
}

func hxEncoding(encoding string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-encoding="%s"`, html.EscapeString(encoding)))
}

func hxDisabledElt(selector string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-disable="%s"`, html.EscapeString(selector)))
}

func hxPreserve(enabled any) template.HTMLAttr {
	switch v := enabled.(type) {
	case bool:
		return template.HTMLAttr(fmt.Sprintf(`hx-preserve="%t"`, v))
	case string:
		return template.HTMLAttr(fmt.Sprintf(`hx-preserve="%s"`, html.EscapeString(v)))
	default:
		return template.HTMLAttr(fmt.Sprintf(`hx-preserve="%v"`, v))
	}
}

func hxReplaceURL(url any) template.HTMLAttr {
	switch v := url.(type) {
	case bool:
		return template.HTMLAttr(fmt.Sprintf(`hx-replace-url="%t"`, v))
	case string:
		return template.HTMLAttr(fmt.Sprintf(`hx-replace-url="%s"`, html.EscapeString(v)))
	default:
		return template.HTMLAttr(fmt.Sprintf(`hx-replace-url="%v"`, v))
	}
}

// hxStatus writes hx-status:pattern="config" with the config HTML-escaped.
// The pattern (a status such as 404 or a range such as 5xx) becomes part of
// the attribute name, so one that does not make a safe attribute name returns
// an error.
func hxStatus(pattern, config string) (template.HTMLAttr, error) {
	parts, err := namedAttrs("hxStatus", "hx-status:", map[string]string{pattern: config})
	if err != nil {
		return "", err
	}
	return template.HTMLAttr(parts[0]), nil
}

// hxAction writes hx-action, the htmx 4 request URL. Pair it with hxMethod
// for the verb.
func hxAction(url string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-action="%s"`, html.EscapeString(url)))
}

// hxMethod writes hx-method, the htmx 4 request verb. Pair it with hxAction
// for the URL.
func hxMethod(method string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-method="%s"`, html.EscapeString(method)))
}

// hxConfig writes hx-config, htmx 4's per-element request config and the
// successor to htmx 2's hx-request, with config JSON-encoded and
// HTML-escaped. An empty or unencodable map writes nothing.
func hxConfig(config map[string]any) template.HTMLAttr {
	if len(config) == 0 {
		return ""
	}
	return jsonAttribute("hx-config", config)
}

// hxOptimistic writes hx-optimistic, which shows content from a template
// before the server responds. It needs htmx 4's optimistic extension.
func hxOptimistic(templateRef string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-optimistic="%s"`, html.EscapeString(templateRef)))
}

// hxPreload writes hx-preload, which starts the request early on an event
// such as "mouseover" or "mousedown". It needs htmx 4's preload extension.
func hxPreload(trigger string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`hx-preload="%s"`, html.EscapeString(trigger)))
}

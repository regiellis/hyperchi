package gomponentsadapter

import (
	"fmt"
	"html/template"
	"io"
	"regexp"
	"strings"

	g "maragu.dev/gomponents"
)

// Attribute is an htmx or hc-* attribute. Its value is HTML-escaped when it
// renders. A name built from caller input (an event or a bound attribute) is
// checked first; an invalid one makes the render fail instead of writing
// markup the caller did not intend.
type Attribute struct {
	name    string
	value   string
	boolean bool
	err     error
}

var _ g.Node = Attribute{}

// eventName is what may follow "hx-on:" or "hc-on:": event names such as
// "click", "htmx:after:swap", or "submit.prevent". It becomes part of the
// attribute name, which cannot be escaped, so anything else is rejected.
var eventName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]*$`)

// attrName is what may follow "hc-bind:": an HTML attribute name.
var attrName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]*$`)

func attr(name, value string) Attribute { return Attribute{name: name, value: value} }

func checked(prefix, suffix string, pattern *regexp.Regexp, value string) Attribute {
	if !pattern.MatchString(suffix) {
		return Attribute{name: prefix + suffix, err: fmt.Errorf("gomponents: invalid name %q for %s", suffix, prefix)}
	}
	return attr(prefix+suffix, value)
}

// Render writes the attribute with a leading space, as gomponents expects.
func (a Attribute) Render(w io.Writer) error {
	if a.err != nil {
		return a.err
	}
	if a.boolean {
		_, err := io.WriteString(w, " "+a.name)
		return err
	}
	_, err := io.WriteString(w, " "+a.name+`="`+template.HTMLEscapeString(a.value)+`"`)
	return err
}

// Type marks the node as an attribute, so gomponents renders it inside the
// element's start tag.
func (Attribute) Type() g.NodeType { return g.AttributeType }

// String renders the attribute, or returns "" when its name is invalid.
func (a Attribute) String() string {
	var b strings.Builder
	if err := a.Render(&b); err != nil {
		return ""
	}
	return b.String()
}

// Name returns the attribute name, such as "hx-get" or "hc-on:click".
func (a Attribute) Name() string { return a.name }

// Inherited returns the htmx 4 inherited form of an hx-* attribute
// ("hx-target:inherited"), which applies it to every descendant. htmx 4 does
// not inherit attributes otherwise.
func (a Attribute) Inherited() Attribute {
	if a.err == nil && !strings.HasPrefix(a.name, "hx-") {
		a.err = fmt.Errorf("gomponents: %s is not an htmx attribute and cannot be inherited", a.name)
	}
	a.name += ":inherited"
	return a
}

// HxGet issues a GET to url (hx-get).
func HxGet(url string) Attribute { return attr("hx-get", url) }

// HxPost issues a POST to url (hx-post).
func HxPost(url string) Attribute { return attr("hx-post", url) }

// HxPut issues a PUT to url (hx-put).
func HxPut(url string) Attribute { return attr("hx-put", url) }

// HxPatch issues a PATCH to url (hx-patch).
func HxPatch(url string) Attribute { return attr("hx-patch", url) }

// HxDelete issues a DELETE to url (hx-delete).
func HxDelete(url string) Attribute { return attr("hx-delete", url) }

// HxTarget picks the element the response swaps into (hx-target).
func HxTarget(selector string) Attribute { return attr("hx-target", selector) }

// HxSwap picks how the response is swapped, such as "outerHTML" (hx-swap).
func HxSwap(strategy string) Attribute { return attr("hx-swap", strategy) }

// HxTrigger picks the events that issue the request (hx-trigger).
func HxTrigger(spec string) Attribute { return attr("hx-trigger", spec) }

// HxSelect picks part of the response to swap (hx-select).
func HxSelect(selector string) Attribute { return attr("hx-select", selector) }

// HxInclude adds the values of other elements to the request (hx-include).
func HxInclude(selector string) Attribute { return attr("hx-include", selector) }

// HxIndicator picks the element that shows while the request runs (hx-indicator).
func HxIndicator(selector string) Attribute { return attr("hx-indicator", selector) }

// HxPushURL pushes a history entry: "true" for the request URL, or a URL (hx-push-url).
func HxPushURL(value string) Attribute { return attr("hx-push-url", value) }

// HxReplaceURL replaces the current history entry (hx-replace-url).
func HxReplaceURL(value string) Attribute { return attr("hx-replace-url", value) }

// HxConfirm asks the user before the request is issued (hx-confirm).
func HxConfirm(message string) Attribute { return attr("hx-confirm", message) }

// HxVals adds values to the request, as a JSON object (hx-vals).
func HxVals(json string) Attribute { return attr("hx-vals", json) }

// HxSync coordinates requests between elements, such as "this:replace" (hx-sync).
func HxSync(spec string) Attribute { return attr("hx-sync", spec) }

// HxEncoding sets the request encoding, such as "multipart/form-data" (hx-encoding).
func HxEncoding(encoding string) Attribute { return attr("hx-encoding", encoding) }

// HxBoost turns links and forms into htmx requests (hx-boost). Use
// HxBoost(true).Inherited() on a container to boost everything inside it.
func HxBoost(on bool) Attribute {
	if on {
		return attr("hx-boost", "true")
	}
	return attr("hx-boost", "false")
}

// HxOn runs script when event fires on the element (hx-on:<event>).
func HxOn(event, script string) Attribute { return checked("hx-on:", event, eventName, script) }

// SSEConnect opens a Server-Sent Events stream with the htmx 4 SSE extension
// (hx-sse:connect). Unnamed messages swap into the element's target.
func SSEConnect(url string) Attribute { return attr("hx-sse:connect", url) }

// SSEClose closes the stream when the server sends the named event (hx-sse:close).
func SSEClose(event string) Attribute {
	if !eventName.MatchString(event) {
		return Attribute{name: "hx-sse:close", err: fmt.Errorf("gomponents: invalid event name %q for hx-sse:close", event)}
	}
	return attr("hx-sse:close", event)
}

// HcData declares a directive scope (hc-data), such as "{open: false}" or a
// registered component call.
func HcData(expr string) Attribute { return attr("hc-data", expr) }

// HcOn runs expr when event fires (hc-on:<event>). Modifiers follow a dot:
// HcOn("submit.prevent", "save()").
func HcOn(event, expr string) Attribute { return checked("hc-on:", event, eventName, expr) }

// HcText sets the element's text to the value of expr (hc-text).
func HcText(expr string) Attribute { return attr("hc-text", expr) }

// HcShow shows the element while expr is truthy (hc-show).
func HcShow(expr string) Attribute { return attr("hc-show", expr) }

// HcBind sets attribute name from expr (hc-bind:<name>), such as
// HcBind("disabled", "!ready").
func HcBind(name, expr string) Attribute { return checked("hc-bind:", name, attrName, expr) }

// HcModel binds a form control to a scope property both ways (hc-model).
func HcModel(expr string) Attribute { return attr("hc-model", expr) }

// HcInit runs expr once when the element is set up (hc-init).
func HcInit(expr string) Attribute { return attr("hc-init", expr) }

// HcRef names the element for $refs in its scope (hc-ref).
func HcRef(name string) Attribute { return attr("hc-ref", name) }

// HcCloak hides the element until the directives have run (hc-cloak).
func HcCloak() Attribute { return Attribute{name: "hc-cloak", boolean: true} }

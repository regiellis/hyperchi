package gomponentsadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

func TestRenderWritesNode(t *testing.T) {
	var out bytes.Buffer
	node := h.P(h.Class("note"), g.Text("Tom & Jerry <3"))
	if err := New().Render(context.Background(), &out, node); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), `<p class="note">Tom &amp; Jerry &lt;3</p>`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestRenderEmptyGroupIsNotAnError(t *testing.T) {
	var out bytes.Buffer
	if err := New().Render(context.Background(), &out, g.Group(nil)); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("empty group wrote %q", out.String())
	}
}

func TestRenderRejectsWhatIsNotANode(t *testing.T) {
	r := New()
	var nilFunc g.NodeFunc
	for name, data := range map[string]any{
		"string":   "<p>hi</p>",
		"nil":      nil,
		"typedNil": nilFunc,
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.Render(context.Background(), io.Discard, data); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestRenderPropagatesNodeErrors(t *testing.T) {
	failure := errors.New("disk on fire")
	node := g.NodeFunc(func(w io.Writer) error {
		if _, err := io.WriteString(w, "<p>partial"); err != nil {
			return err
		}
		return failure
	})
	if err := New().Render(context.Background(), io.Discard, node); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
}

func TestRenderStopsOnDoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rendered := false
	node := g.NodeFunc(func(io.Writer) error { rendered = true; return nil })
	if err := New().Render(ctx, io.Discard, node); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if rendered {
		t.Fatal("rendered after the context ended")
	}
}

func TestHTML(t *testing.T) {
	out, err := HTML(h.Span(g.Text(`"quoted"`)))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `<span>&#34;quoted&#34;</span>` {
		t.Fatalf("got %s", out)
	}
	if _, err := HTML(nil); err == nil {
		t.Fatal("nil accepted")
	}
	failure := errors.New("boom")
	if _, err := HTML(g.NodeFunc(func(io.Writer) error { return failure })); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
}

func render(t *testing.T, n g.Node) string {
	t.Helper()
	var b strings.Builder
	if err := n.Render(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestAttributesRenderInStartTag(t *testing.T) {
	got := render(t, h.Button(
		HxPost("/lists/1/import"), HxTarget("#result"), HxSwap("outerHTML"),
		HxTrigger("click"), HxConfirm("Import?"), HcOn("click.prevent", "open = !open"),
		HcBind("disabled", "!ready"), HcCloak(), g.Text("Go"),
	))
	want := `<button hx-post="/lists/1/import" hx-target="#result" hx-swap="outerHTML" hx-trigger="click"` +
		` hx-confirm="Import?" hc-on:click.prevent="open = !open" hc-bind:disabled="!ready" hc-cloak>Go</button>`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestAttributeValuesAreEscaped(t *testing.T) {
	cases := map[string]Attribute{
		`hx-get="/search?q=a&amp;b=&#34;&lt;script&gt;"`:   HxGet(`/search?q=a&b="<script>`),
		`hx-vals="{&#34;id&#34;:&#34;1&#39; x&#34;}"`:      HxVals(`{"id":"1' x"}`),
		`hc-data="{name: &#39;&lt;/div&gt;&#39;}"`:         HcData(`{name: '</div>'}`),
		`hx-on:htmx:after:swap="alert(&#34;x&#34;)"`:       HxOn("htmx:after:swap", `alert("x")`),
		`hx-sse:connect="/events?x=&#34;&gt;&lt;img&#34;"`: SSEConnect(`/events?x="><img"`),
	}
	for want, a := range cases {
		if got := strings.TrimSpace(render(t, a)); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestUnsafeNamesFailTheRender(t *testing.T) {
	for _, a := range []Attribute{
		HxOn(`click" onmouseover="x`, "y"),
		HcOn("", "y"),
		HcOn("a b", "y"),
		HcBind(`x=1 onclick`, "y"),
		HcBind("1abc", "y"),
		SSEClose(`done"`),
		HcData("{}").Inherited(),
	} {
		if err := a.Render(io.Discard); err == nil {
			t.Errorf("%s rendered", a.Name())
		}
		if a.String() != "" {
			t.Errorf("%s String = %q", a.Name(), a.String())
		}
		var b strings.Builder
		if err := h.Div(a).Render(&b); err == nil {
			t.Errorf("element with %s rendered %q", a.Name(), b.String())
		}
	}
}

func TestInheritedAndBoolean(t *testing.T) {
	if got := strings.TrimSpace(render(t, HxTarget("#main").Inherited())); got != `hx-target:inherited="#main"` {
		t.Fatalf("got %s", got)
	}
	if got := strings.TrimSpace(render(t, HxBoost(true).Inherited())); got != `hx-boost:inherited="true"` {
		t.Fatalf("got %s", got)
	}
	if got := strings.TrimSpace(render(t, HxBoost(false))); got != `hx-boost="false"` {
		t.Fatalf("got %s", got)
	}
	if got := SSEClose("done").String(); got != ` hx-sse:close="done"` {
		t.Fatalf("got %s", got)
	}
}

func TestEveryHelperNamesItsAttribute(t *testing.T) {
	cases := map[string]Attribute{
		"hx-get": HxGet("u"), "hx-post": HxPost("u"), "hx-put": HxPut("u"), "hx-patch": HxPatch("u"),
		"hx-delete": HxDelete("u"), "hx-target": HxTarget("s"), "hx-swap": HxSwap("s"),
		"hx-trigger": HxTrigger("s"), "hx-select": HxSelect("s"), "hx-include": HxInclude("s"),
		"hx-indicator": HxIndicator("s"), "hx-push-url": HxPushURL("true"), "hx-replace-url": HxReplaceURL("true"),
		"hx-confirm": HxConfirm("m"), "hx-vals": HxVals("{}"), "hx-sync": HxSync("this:replace"),
		"hx-encoding": HxEncoding("multipart/form-data"), "hx-boost": HxBoost(true), "hx-on:click": HxOn("click", "x"),
		"hx-sse:connect": SSEConnect("/e"), "hx-sse:close": SSEClose("done"), "hc-data": HcData("{}"),
		"hc-on:input": HcOn("input", "x"), "hc-text": HcText("x"), "hc-show": HcShow("x"),
		"hc-bind:href": HcBind("href", "x"), "hc-model": HcModel("x"), "hc-init": HcInit("x"),
		"hc-ref": HcRef("x"), "hc-cloak": HcCloak(),
	}
	for name, a := range cases {
		if a.Name() != name {
			t.Errorf("got %s, want %s", a.Name(), name)
		}
		if a.Type() != g.AttributeType {
			t.Errorf("%s is not an attribute node", name)
		}
		if !strings.HasPrefix(a.String(), " "+name) {
			t.Errorf("%s renders as %q", name, a.String())
		}
	}
}

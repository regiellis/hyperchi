package hyperchi

import (
	"bytes"
	"html"
	"html/template"
	"strings"
	"testing"
)

// AnimeData escapes the preset name, so a value with quotes or a tag cannot
// end the attribute or add another one.
func TestAnimeDataEscapesPreset(t *testing.T) {
	got := string(AnimeData(`x" onclick="alert(1)'<b>&`))
	want := `data-anime-preset="x&#34; onclick=&#34;alert(1)&#39;&lt;b&gt;&amp;"`
	if got != want {
		t.Errorf("AnimeData = %s, want %s", got, want)
	}
}

// ToHTMLAttr is a whole data-anime attribute whose value is the escaped JSON,
// so it is safe where html/template trusts an HTMLAttr: <div {{.anim}}>.
func TestToHTMLAttrEscapesJSON(t *testing.T) {
	anim := NewAnimation(`a" onmouseover="alert(1)`).Set(`it's<b>`, `"x"`)
	attr := string(anim.ToHTMLAttr())

	const prefix = `data-anime="`
	if !strings.HasPrefix(attr, prefix) || !strings.HasSuffix(attr, `"`) {
		t.Fatalf("ToHTMLAttr = %s, want a data-anime=\"...\" attribute", attr)
	}
	value := attr[len(prefix) : len(attr)-1]
	if strings.ContainsAny(value, "\"<>'") {
		t.Errorf("attribute value has an unescaped quote or bracket: %s", value)
	}
	want, err := anim.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := html.UnescapeString(value); got != want {
		t.Errorf("unescaped value = %s, want the ToJSON output %s", got, want)
	}

	// html/template trusts an HTMLAttr in the attribute position and writes it
	// unchanged, so the escaping above is all that keeps it one attribute.
	tmpl := template.Must(template.New("t").Parse(`<div {{.}}></div>`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, anim.ToHTMLAttr()); err != nil {
		t.Fatal(err)
	}
	if want := "<div " + attr + "></div>"; buf.String() != want {
		t.Errorf("rendered %s, want %s", buf.String(), want)
	}
}

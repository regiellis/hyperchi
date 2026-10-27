package main

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestBuiltWithPageListsEverySite(t *testing.T) {
	app := docsApp(t, testOrigin)
	w := get(t, app, "/showcase", false)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, site := range builtWithSites {
		for _, want := range []string{`href="` + site.URL + `"`, site.Name, site.Host} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page is missing %q", site.Name, want)
			}
		}
	}
	if !strings.Contains(body, `<a href="/showcase" aria-current="page">Built with</a>`) {
		t.Error("nav does not mark Built with as the current page")
	}
	if !strings.Contains(body, `<meta name="description" content="Sites running on HyperChi in production`) {
		t.Error("page description did not replace the site default")
	}
	if strings.Contains(body, "—") {
		t.Error("visible copy contains an em-dash")
	}
}

// The screenshots ship embedded; a missing one fails the {{asset}} render, but
// this names the file.
func TestBuiltWithImagesAreEmbedded(t *testing.T) {
	for _, site := range builtWithSites {
		name := "static" + strings.TrimPrefix(site.Image, "/static")
		if _, err := fs.Stat(publicAssets, name); err != nil {
			t.Errorf("%s: %v", site.Name, err)
		}
	}
}

func TestOtherPagesKeepTheDefaultDescription(t *testing.T) {
	app := docsApp(t, testOrigin)
	body := get(t, app, "/", false).Body.String()
	if !strings.Contains(body, `<meta name="description" content="Build expressive web applications`) {
		t.Error("home page lost the default description")
	}
}

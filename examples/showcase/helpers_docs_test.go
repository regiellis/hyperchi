package main

import (
	"bytes"
	"strings"
	"testing"

	"showcase/internal/apiref"
)

// The embedded reference must match the framework source; regenerate it with
// go generate when this fails.
func TestHelpersReferenceIsCurrent(t *testing.T) {
	groups, err := apiref.Build("../..")
	if err != nil {
		t.Fatal(err)
	}
	want, err := apiref.Encode(groups)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, helpersReferenceJSON) {
		t.Fatal("reference/helpers.json is stale: run `go generate ./...` in examples/showcase")
	}
}

func TestHelpersPageListsEveryHelperByCategory(t *testing.T) {
	groups, err := helpersReference()
	if err != nil {
		t.Fatal(err)
	}
	app := docsApp(t, testOrigin)
	page := get(t, app, "/docs/helpers", false).Body.String()
	for _, g := range groups {
		if !strings.Contains(page, "<summary>"+g.Name+" <span>") {
			t.Errorf("group %q missing from the page", g.Name)
		}
		for _, e := range g.Entries {
			if !strings.Contains(page, `id="helper-`+e.Name+`"`) {
				t.Errorf("%s missing from the page", e.Name)
			}
		}
	}
	md := get(t, app, "/docs/helpers.md", false).Body.String()
	for _, name := range []string{"`Context.Param`", "`Bind`", "`Str.Upper`", "`Must`"} {
		if !strings.Contains(md, name) {
			t.Errorf("Markdown reference missing %s", name)
		}
	}
	results := get(t, app, "/docs/search?q=ReadFileOr", true).Body.String()
	if !strings.Contains(results, "/docs/helpers") {
		t.Error("search does not find a helper by name")
	}
}

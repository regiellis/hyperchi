package main

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestParseValidRows(t *testing.T) {
	src := "\uFEFFEmail, Name ,First Name,Company\n" +
		"Ada@Example.com,Ada Lovelace,Ada,Analytical Engines\n" +
		"bob@example.org,Bob,, \n" +
		"\n" +
		"carol@example.net,,Carol,\"Smith, Jones & Co\"\n"
	p, err := parseRecipientsCSV(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Columns, ","); got != "first_name,company" {
		t.Fatalf("columns %s", got)
	}
	if len(p.Rows) != 3 || len(p.Problems) != 0 || len(p.Duplicates) != 0 {
		t.Fatalf("rows %d problems %v duplicates %v", len(p.Rows), p.Problems, p.Duplicates)
	}
	ada := p.Rows[0]
	if ada.Email != "ada@example.com" || ada.Name != "Ada Lovelace" || ada.Fields["first_name"] != "Ada" || ada.Fields["company"] != "Analytical Engines" {
		t.Fatalf("ada %+v", ada)
	}
	if _, ok := p.Rows[1].Fields["company"]; ok {
		t.Fatal("a blank value should not become a field")
	}
	if p.Rows[2].Fields["company"] != "Smith, Jones & Co" || p.Rows[2].Line != 5 {
		t.Fatalf("carol %+v", p.Rows[2])
	}
}

func TestParseReportsInvalidRowsAndKeepsTheRest(t *testing.T) {
	src := "email,name\n" +
		"good@example.com,Good\n" +
		"not-an-email,Bad\n" +
		"Display Name <x@example.com>,Display\n" +
		"nodot@localhost,Local\n" +
		",Empty\n" +
		"extra@example.com,Extra,column\n" +
		"fine@example.org,Fine\n"
	p, err := parseRecipientsCSV(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 2 {
		t.Fatalf("rows %+v", p.Rows)
	}
	want := map[int]string{3: "Not a valid email", 4: "Not a valid email", 5: "Not a valid email", 6: "Email is empty", 7: "Expected 2 columns"}
	if len(p.Problems) != len(want) {
		t.Fatalf("problems %+v", p.Problems)
	}
	for _, pr := range p.Problems {
		if !strings.HasPrefix(pr.Message, want[pr.Line]) {
			t.Errorf("line %d: %q, want %q", pr.Line, pr.Message, want[pr.Line])
		}
	}
}

func TestParseDedupesWithinTheFile(t *testing.T) {
	src := "email,name\na@example.com,First\nA@EXAMPLE.COM,Second\nb@example.com,B\na@example.com,Third\n"
	p, err := parseRecipientsCSV(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 2 || p.Rows[0].Name != "First" {
		t.Fatalf("rows %+v", p.Rows)
	}
	if len(p.Duplicates) != 2 || p.Duplicates[0].Line != 3 || !strings.Contains(p.Duplicates[0].Message, "line 2") {
		t.Fatalf("duplicates %+v", p.Duplicates)
	}
}

func TestParseRejectsUnusableFiles(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"no email":     "name,company\nAda,X\n",
		"bad header":   "email,???\na@example.com,x\n",
		"dup header":   "email,Company,company\na@example.com,x,y\n",
		"not utf8":     "email\n\xff\xfe@example.com\n",
		"broken quote": "\"email\nfoo",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseRecipientsCSV(strings.NewReader(src))
			var bad errHeader
			if !errors.As(err, &bad) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestParseCapsValueSizesAndRowCount(t *testing.T) {
	long := strings.Repeat("x", maxFieldValue+1)
	p, err := parseRecipientsCSV(strings.NewReader("email,note\na@example.com," + long + "\nb@example.com,short\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || len(p.Problems) != 1 || !strings.Contains(p.Problems[0].Message, "note") {
		t.Fatalf("rows %d problems %+v", len(p.Rows), p.Problems)
	}
	var b strings.Builder
	b.WriteString("email\n")
	for i := range maxImportRows + 1 {
		fmt.Fprintf(&b, "p%d@example.com\n", i)
	}
	var bad errHeader
	if _, err := parseRecipientsCSV(strings.NewReader(b.String())); !errors.As(err, &bad) {
		t.Fatalf("over the row limit: %v", err)
	}
}

// upload posts a CSV the way the import form does: multipart, with htmx's
// CSRF header.
func (a *testApp) upload(path, filename string, content []byte) (*http.Response, string) {
	a.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		a.t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		a.t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		a.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+path, &body)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", a.csrf())
	return a.do(req)
}

func TestImportUpload(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Customers", 1, nil) // person000@example.com is already on it
	path := fmt.Sprintf("/lists/%d/import", listID)
	csv := "email,name,city\nperson000@example.com,Already Here,Oslo\nnew@example.com,New,Rome\nNEW@example.com,Dup,Rome\nbroken,Bad,\n"
	res, body := a.upload(path, "people.csv", []byte(csv))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if res.Header.Get("HX-Trigger") != "recipients-changed" {
		t.Fatalf("HX-Trigger %q", res.Header.Get("HX-Trigger"))
	}
	for _, want := range []string{"1 recipient added.", "1 already on Customers", "1 duplicate in the file skipped", "1 row rejected", "{{city}}", "Not a valid email address."} {
		if !strings.Contains(body, want) {
			t.Errorf("result lacks %q: %s", want, body)
		}
	}
	if n := a.count("recipients", "list_id = ?", listID); n != 2 {
		t.Fatalf("%d recipients", n)
	}
	r, err := one[Recipient](a.s.db, `SELECT * FROM recipients WHERE email = 'person000@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "Person 0" {
		t.Fatalf("existing recipient was overwritten: %+v", r)
	}
	if n := a.count("audit_log", "action = 'imported recipients'"); n != 1 {
		t.Fatalf("%d audit rows", n)
	}

	res, body = a.upload(path, "bad.csv", []byte("name\nNo Email\n"))
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "no email column") {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
}

func TestImportRejectsOversizeUploads(t *testing.T) {
	a := newTestApp(t, func(o *Options) { o.MaxUpload = 1024 })
	a.signIn()
	listID := a.seedList("Big", 0, nil)
	var b strings.Builder
	b.WriteString("email\n")
	for i := 0; b.Len() < 4096; i++ {
		fmt.Fprintf(&b, "p%d@example.com\n", i)
	}
	res, body := a.upload(fmt.Sprintf("/lists/%d/import", listID), "big.csv", []byte(b.String()))
	if res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(body, "larger than 1 KB") {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if n := a.count("recipients", "list_id = ?", listID); n != 0 {
		t.Fatalf("%d recipients added from an oversize file", n)
	}
}

// TestImportWithoutJavaScript posts the upload form the way a browser does
// with scripts off: multipart, with the token from the page's csrf_token
// field and no htmx headers. The answer is a whole page around the result.
func TestImportWithoutJavaScript(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Walk-ins", 0, nil)
	page := fmt.Sprintf("/lists/%d", listID)
	_, html := a.get(page, false)
	m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("the list page has no csrf_token field: %s", html)
	}
	// More page loads before the submit must not invalidate the rendered token.
	a.get("/lists", false)
	a.get(page+"?q=x", true)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("csrf_token", m[1]); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile("file", "walk-ins.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("email,name\nada@example.com,Ada\ngrace@example.com,Grace\n")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+page+"/import", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, out := a.do(req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, out)
	}
	for _, want := range []string{"<!doctype html>", "2 recipients added.", `href="` + page + `"`} {
		if !strings.Contains(out, want) {
			t.Errorf("response lacks %q: %s", want, out)
		}
	}
	if n := a.count("recipients", "list_id = ?", listID); n != 2 {
		t.Fatalf("%d recipients imported", n)
	}
}

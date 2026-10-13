package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"strings"
	"testing"
)

// renderedSendout runs a job to completion and returns its id.
func renderedSendout(a *testApp, format, body string, names map[int]string) int64 {
	a.t.Helper()
	listID := a.seedList("Export", 3, nil)
	for i, name := range names {
		if _, err := a.s.db.Exec(`UPDATE recipients SET name = ? WHERE list_id = ? AND email = ?`,
			name, listID, fmt.Sprintf("person%03d@example.com", i)); err != nil {
			a.t.Fatal(err)
		}
	}
	sid := a.seedSendout(a.seedLetter("Hello {{name}}", body, format, false), listID)
	if _, err := a.s.jobs.Start(sid); err != nil {
		a.t.Fatal(err)
	}
	a.waitJob(sid)
	return sid
}

func TestExportCSV(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	sid := renderedSendout(a, formatText, "Dear {{name}}", map[int]string{1: "=HYPERLINK(\"x\")"})
	res, body := a.get(fmt.Sprintf("/sendouts/%d/export.csv", sid), false)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("%d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if cd := res.Header.Get("Content-Disposition"); cd != fmt.Sprintf("attachment; filename=sendout-%d.csv", sid) {
		t.Fatalf("disposition %q", cd)
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || strings.Join(rows[0], ",") != "id,email,name,status,subject,body,format,error,rendered_at" {
		t.Fatalf("rows %v", rows)
	}
	if rows[1][1] != "person000@example.com" || rows[1][3] != "rendered" || rows[1][5] != "Dear Person 0" {
		t.Fatalf("row %v", rows[1])
	}
	if rows[2][2] != `'=HYPERLINK("x")` {
		t.Fatalf("a formula reached the spreadsheet unguarded: %q", rows[2][2])
	}
}

func TestExportEMLZip(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	sid := renderedSendout(a, formatHTML, "<p>Dear {{name}}</p>", map[int]string{0: "Zoë <b>Bold</b>"})
	res, body := a.get(fmt.Sprintf("/sendouts/%d/export.zip", sid), false)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("%d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 3 || !strings.HasSuffix(zr.File[0].Name, "-person000_example.com.eml") {
		t.Fatalf("files %v", zr.File)
	}
	f, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if subject != "Hello Zoë <b>Bold</b>" {
		t.Fatalf("subject %q", subject)
	}
	to, err := mail.ParseAddress(msg.Header.Get("To"))
	if err != nil || to.Address != "person000@example.com" || to.Name != "Zoë <b>Bold</b>" {
		t.Fatalf("to %v %v", to, err)
	}
	if msg.Header.Get("Content-Type") != "text/html; charset=utf-8" || msg.Header.Get("Message-ID") == "" {
		t.Fatalf("headers %v", msg.Header)
	}
	html, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	if string(html) != "<p>Dear Zoë &lt;b&gt;Bold&lt;/b&gt;</p>" {
		t.Fatalf("body %q", html)
	}

	item, err := one[OutboxItem](a.s.db, `SELECT * FROM outbox WHERE sendout_id = ? ORDER BY id LIMIT 1`, sid)
	if err != nil {
		t.Fatal(err)
	}
	res, body = a.get(fmt.Sprintf("/outbox/%d/eml", item.ID), false)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "message/rfc822" || !strings.HasPrefix(body, "From: ") {
		t.Fatalf("single .eml: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if n := a.count("audit_log", "action LIKE 'exported%'"); n != 1 {
		t.Fatalf("%d export audit rows", n)
	}
}

func TestPagesRender(t *testing.T) {
	a := newTestApp(t, func(o *Options) { o.SeedDemo = true })
	a.signIn()
	sid := renderedSendout(a, formatText, "Dear {{name}}", nil)
	for _, path := range []string{
		"/lists", "/lists/1", "/lists/1?q=ada&page=1", "/letters", "/letters/new", "/letters/1",
		"/sendouts", "/sendouts/new?letter_id=1&list_id=1", fmt.Sprintf("/sendouts/%d", sid),
		fmt.Sprintf("/sendouts/%d/outbox", sid), fmt.Sprintf("/sendouts/%d/outbox?status=rendered", sid),
		"/outbox/1", "/team", "/audit", "/static/css/app.css", "/hyperchi/js/hx-sse.js",
	} {
		res, body := a.get(path, false)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, res.StatusCode, body)
		}
	}
	res, body := a.get("/lists/999", false)
	if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "No such list.") || !strings.Contains(body, "<!doctype html>") {
		t.Fatalf("missing list page: %d %s", res.StatusCode, body)
	}
	res, body = a.get("/lists/999", true)
	if res.StatusCode != http.StatusNotFound || strings.Contains(body, "<html") || !strings.Contains(body, `role="alert"`) {
		t.Fatalf("missing list fragment: %d %s", res.StatusCode, body)
	}
	res, body = a.get("/lists/1?q=ada", true)
	if res.StatusCode != http.StatusOK || strings.Contains(body, "<html") || !strings.Contains(body, `id="recipient-table"`) {
		t.Fatalf("search fragment: %d %s", res.StatusCode, body)
	}
	if res, _ := a.get("/static/../server.go", false); res.StatusCode != http.StatusNotFound {
		t.Fatalf("static escape: %d", res.StatusCode)
	}
}

// TestRunsFromAnyWorkingDirectory renders pages and serves assets from a
// directory with no static/ in it: the assets are embedded and the pages are
// gomponents, so nothing is read from the working directory.
func TestRunsFromAnyWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	a := newTestApp(t, func(o *Options) { o.SeedDemo = true })
	a.signIn()
	res, body := a.get("/lists/1", false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `href="/static/css/app.css"`) {
		t.Fatalf("list page: %d %s", res.StatusCode, body)
	}
	for _, path := range []string{"/static/css/app.css", "/static/js/app.js", "/static/js/htmx.min.js", "/static/favicon.svg"} {
		res, body := a.get(path, false)
		if res.StatusCode != http.StatusOK || body == "" || res.Header.Get("ETag") == "" {
			t.Errorf("%s: %d, %d bytes, ETag %q", path, res.StatusCode, len(body), res.Header.Get("ETag"))
		}
		if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=300" {
			t.Errorf("%s: Cache-Control %q", path, cc)
		}
	}
	for _, path := range []string{"/static/", "/static/js/", "/static/missing.js"} {
		if res, _ := a.get(path, false); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, res.StatusCode)
		}
	}
}

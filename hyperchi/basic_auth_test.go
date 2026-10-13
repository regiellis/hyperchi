package hyperchi

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBasicAuthRefusesThroughThePipelineWithoutLoggingTheUser(t *testing.T) {
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	app := New()
	app.BasicAuth("admin", "s3cret-pass")
	var seen int
	app.OnError(func(c *Context, err error) {
		if he, ok := err.(*HTTPError); ok {
			seen = he.Code
			if werr := c.Status(he.Code).Text("handled"); werr != nil {
				t.Error(werr)
			}
		}
	})
	app.Get("/", func(c *Context) error { return c.Text("in") })

	for _, creds := range [][2]string{{"", ""}, {"admin", "wrong"}, {"admin", "s3cret-pas"}, {"Admin", "s3cret-pass"}} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if creds[0] != "" {
			req.SetBasicAuth(creds[0], creds[1])
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" || rec.Body.String() != "handled" {
			t.Fatalf("%v: got %d %q %v", creds, rec.Code, rec.Body.String(), rec.Header())
		}
	}
	if seen != http.StatusUnauthorized {
		t.Fatalf("OnError saw %d", seen)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "s3cret-pass")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "in" {
		t.Fatalf("valid credentials = %d %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(logs.String(), "admin") {
		t.Fatalf("the username reached the log: %q", logs.String())
	}

	defer func() {
		if recover() == nil {
			t.Fatal("empty credentials were accepted")
		}
	}()
	New().BasicAuth("admin", "")
}

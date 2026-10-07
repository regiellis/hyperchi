package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

// testApp is a running mailgrid behind an httptest server, with a client
// that keeps cookies and does not follow redirects.
type testApp struct {
	t      *testing.T
	s      *Server
	srv    *httptest.Server
	client *http.Client
	stop   context.CancelFunc
	user   *User
}

func newTestApp(t *testing.T, tune func(*Options)) *testApp {
	t.Helper()
	cfg := config.Default()
	cfg.Database.Name = filepath.Join(t.TempDir(), "mailgrid.db")
	cfg.Assets.HotReload = false
	ctx, cancel := context.WithCancel(context.Background())
	opts := Options{Config: cfg, Shutdown: ctx}
	if tune != nil {
		tune(&opts)
	}
	s, err := NewServer(opts)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s.jobs.finished = make(chan int64, 16)
	if err := s.app.Integrations().Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.app)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &testApp{t: t, s: s, srv: srv, stop: cancel, client: &http.Client{
		Jar:           jar,
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	t.Cleanup(func() {
		cancel() // ends running jobs and open event streams
		srv.Close()
		if err := s.app.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

// signIn creates a user and a session for it without going through bcrypt;
// the login test covers the real sign-in.
func (a *testApp) signIn() *User {
	a.t.Helper()
	id, err := a.s.db.Create("users", map[string]any{"email": "ana@example.com", "name": "Ana", "password_hash": "unused"})
	if err != nil {
		a.t.Fatal(err)
	}
	u, err := one[User](a.s.db, `SELECT * FROM users WHERE id = ?`, id)
	if err != nil {
		a.t.Fatal(err)
	}
	session, err := hyperchi.NewKVSession(a.s.app.KV("sessions"), "session:", time.Hour)
	if err != nil {
		a.t.Fatal(err)
	}
	session.Set("uid", strconv.FormatInt(u.ID, 10))
	if err := session.Save(); err != nil {
		a.t.Fatal(err)
	}
	base, err := url.Parse(a.srv.URL)
	if err != nil {
		a.t.Fatal(err)
	}
	a.client.Jar.SetCookies(base, []*http.Cookie{{Name: "mailgrid_session", Value: session.ID, Path: "/"}})
	a.user = &u
	return &u
}

func (a *testApp) do(req *http.Request) (*http.Response, string) {
	a.t.Helper()
	res, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		a.t.Fatal(err)
	}
	return res, string(body)
}

func (a *testApp) get(path string, htmx bool) (*http.Response, string) {
	a.t.Helper()
	req, err := http.NewRequest(http.MethodGet, a.srv.URL+path, nil)
	if err != nil {
		a.t.Fatal(err)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return a.do(req)
}

// csrf returns the token cookie a page load issued, loading one if needed.
func (a *testApp) csrf() string {
	a.t.Helper()
	base, err := url.Parse(a.srv.URL)
	if err != nil {
		a.t.Fatal(err)
	}
	find := func() string {
		for _, c := range a.client.Jar.Cookies(base) {
			if c.Name == "csrf_token" {
				return c.Value
			}
		}
		return ""
	}
	if tok := find(); tok != "" {
		return tok
	}
	a.get("/login", false)
	return find()
}

// post sends a form the way the app's htmx forms do: with the CSRF header.
func (a *testApp) post(path string, form url.Values) (*http.Response, string) {
	a.t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", a.csrf())
	return a.do(req)
}

// seedList creates a list with n recipients, each with a company field
// except where skip says otherwise.
func (a *testApp) seedList(name string, n int, fields func(i int) map[string]string) int64 {
	a.t.Helper()
	listID, err := a.s.db.Create("lists", map[string]any{"name": name})
	if err != nil {
		a.t.Fatal(err)
	}
	for i := range n {
		f := map[string]string{"company": fmt.Sprintf("Firm %d", i)}
		if fields != nil {
			f = fields(i)
		}
		encoded, err := json.Marshal(f)
		if err != nil {
			a.t.Fatal(err)
		}
		if _, err := a.s.db.Create("recipients", map[string]any{
			"list_id": listID, "email": fmt.Sprintf("person%03d@example.com", i), "name": fmt.Sprintf("Person %d", i),
			"fields": string(encoded),
		}); err != nil {
			a.t.Fatal(err)
		}
	}
	return listID
}

func (a *testApp) seedLetter(subject, body, format string, strict bool) int64 {
	a.t.Helper()
	id, err := a.s.db.Create("letters", map[string]any{
		"title": "Test letter", "subject": subject, "body": body, "format": format, "strict": strict,
	})
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

func (a *testApp) seedSendout(letterID, listID int64) int64 {
	a.t.Helper()
	id, err := a.s.db.Create("sendouts", map[string]any{"letter_id": letterID, "list_id": listID, "status": statusNew})
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

// waitJob blocks until the job for id stops, failing after a deadline.
func (a *testApp) waitJob(id int64) {
	a.t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case done := <-a.s.jobs.finished:
			if done == id {
				return
			}
		case <-deadline.C:
			a.t.Fatalf("send-out %d did not finish", id)
		}
	}
}

func (a *testApp) progress(id int64) Progress {
	a.t.Helper()
	p, err := loadProgress(context.Background(), a.s.db, id)
	if err != nil {
		a.t.Fatal(err)
	}
	return p
}

func (a *testApp) count(table, where string, args ...any) int {
	a.t.Helper()
	n, err := a.s.db.Count(table, where, args...)
	if err != nil {
		a.t.Fatal(err)
	}
	return n
}

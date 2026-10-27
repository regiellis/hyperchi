package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	cws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

// script is a Roller that returns prepared rolls in order.
type script struct {
	mu    sync.Mutex
	rolls [][]int
}

func newScript(rolls ...[]int) *script { return &script{rolls: rolls} }

func (s *script) roll(n int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rolls) == 0 {
		return nil, fmt.Errorf("script: no roll left for %d dice", n)
	}
	r := s.rolls[0]
	s.rolls = s.rolls[1:]
	if len(r) != n {
		return nil, fmt.Errorf("script: next roll has %d dice, game asked for %d", len(r), n)
	}
	return r, nil
}

// timers records grace timers instead of starting them, so a test decides
// when a seat expires.
type timers struct {
	mu    sync.Mutex
	funcs []func()
}

func (tm *timers) after(_ time.Duration, f func()) func() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.funcs = append(tm.funcs, f)
	return func() bool { return true }
}

// fireLast runs the most recent timer, as if the grace window ran out.
func (tm *timers) fireLast(t *testing.T) {
	t.Helper()
	tm.mu.Lock()
	if len(tm.funcs) == 0 {
		tm.mu.Unlock()
		t.Fatal("no grace timer was started")
	}
	f := tm.funcs[len(tm.funcs)-1]
	tm.mu.Unlock()
	f()
}

// testServer runs the whole app, in development mode, behind httptest.
func testServer(t *testing.T, opts Options) (*httptest.Server, *Arena) {
	t.Helper()
	app, arena, err := newApp(config.Default(), appOptions{Arena: opts})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv, arena
}

// player is one browser: a cookie jar and an HTTP client that does not
// follow redirects, so tests can read them. csrf is the token the lobby page
// gave htmx, sent back on every post as the X-CSRF-Token header.
type player struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	csrf   string
}

// csrfHeader finds the token the layout puts in hx-headers on <body>.
var csrfHeader = regexp.MustCompile(`hx-headers:inherited='\{"X-CSRF-Token": "([^"]+)"\}'`)

func pageToken(body string) string {
	m := csrfHeader.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

func newPlayer(t *testing.T, srv *httptest.Server, name string) *player {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &player{t: t, srv: srv, client: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	body, resp := p.get("/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Pick a name") {
		t.Fatalf("lobby: %d", resp.StatusCode)
	}
	if p.csrf = pageToken(body); p.csrf == "" {
		t.Fatal("the lobby page gave htmx no CSRF token")
	}
	if name != "" {
		if _, resp := p.post("/name", url.Values{"name": {name}}, true); resp.StatusCode != http.StatusOK {
			t.Fatalf("set name %q: %d", name, resp.StatusCode)
		}
	}
	return p
}

func (p *player) do(req *http.Request) (string, *http.Response) {
	p.t.Helper()
	resp, err := p.client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	return readAll(p.t, resp), resp
}

func (p *player) get(path string) (string, *http.Response) {
	p.t.Helper()
	req, err := http.NewRequest(http.MethodGet, p.srv.URL+path, nil)
	if err != nil {
		p.t.Fatal(err)
	}
	return p.do(req)
}

func (p *player) post(path string, form url.Values, htmx bool) (string, *http.Response) {
	p.t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.csrf != "" {
		req.Header.Set("X-CSRF-Token", p.csrf)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return p.do(req)
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// createRoom opens a table and returns its code.
func (p *player) createRoom(target int) string {
	p.t.Helper()
	_, resp := p.post("/rooms", url.Values{"target": {fmt.Sprint(target)}}, false)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/r/") {
		p.t.Fatalf("create room: %d %q", resp.StatusCode, loc)
	}
	return strings.TrimPrefix(loc, "/r/")
}

func (p *player) join(code string) {
	p.t.Helper()
	_, resp := p.post("/join", url.Values{"code": {code}}, false)
	if resp.StatusCode != http.StatusSeeOther {
		p.t.Fatalf("join %s: %d", code, resp.StatusCode)
	}
}

func (p *player) cookieHeader() string {
	u, err := url.Parse(p.srv.URL)
	if err != nil {
		p.t.Fatal(err)
	}
	parts := []string{}
	for _, c := range p.client.Jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// dial opens a WebSocket with the player's cookies, as the browser would.
func (p *player) dial(ctx context.Context, path string, header http.Header) (*cws.Conn, error) {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Cookie", p.cookieHeader())
	conn, resp, err := cws.Dial(ctx, "ws"+strings.TrimPrefix(p.srv.URL, "http")+path, &cws.DialOptions{HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		if cerr := resp.Body.Close(); cerr != nil {
			p.t.Errorf("close handshake body: %v", cerr)
		}
	}
	return conn, err
}

// socket is a dialed page with a helper to read pushes.
type socket struct {
	t    *testing.T
	ctx  context.Context
	conn *cws.Conn
}

func (p *player) connect(ctx context.Context, path string) *socket {
	p.t.Helper()
	conn, err := p.dial(ctx, path, nil)
	if err != nil {
		p.t.Fatalf("dial %s: %v", path, err)
	}
	p.t.Cleanup(func() {
		if err := conn.CloseNow(); err != nil && !strings.Contains(err.Error(), "closed") {
			p.t.Logf("close socket: %v", err)
		}
	})
	return &socket{t: p.t, ctx: ctx, conn: conn}
}

// frame is one push from the server, as the htmx ws extension reads it.
type frame struct {
	Type    string `json:"type"`
	Target  string `json:"target"`
	Swap    string `json:"swap"`
	Content string `json:"content"`
}

func (f frame) String() string { return f.Target + " " + f.Content }

// until reads pushes until one is a swap into target containing want, and
// returns it with every frame read before it. It fails at the deadline, so
// no test sleeps to wait for the server.
func (s *socket) until(target, want string) (frame, []frame) {
	s.t.Helper()
	var seen []frame
	for {
		var f frame
		if err := wsjson.Read(s.ctx, s.conn, &f); err != nil {
			s.t.Fatalf("waiting for %s containing %q: %v\nseen: %v", target, want, err, seen)
		}
		if f.Type == "htmx" && f.Target == target && strings.Contains(f.Content, want) {
			return f, seen
		}
		seen = append(seen, f)
	}
}

// next reads pushes until one targets target and returns it undecoded, so a
// test can tell an empty content field from a missing one.
func (s *socket) next(target string) map[string]any {
	s.t.Helper()
	for {
		var msg map[string]any
		if err := wsjson.Read(s.ctx, s.conn, &msg); err != nil {
			s.t.Fatalf("waiting for a push to %s: %v", target, err)
		}
		if msg["target"] == target {
			return msg
		}
	}
}

func (s *socket) send(v any) {
	s.t.Helper()
	if err := wsjson.Write(s.ctx, s.conn, v); err != nil {
		s.t.Fatal(err)
	}
}

func (s *socket) close() {
	s.t.Helper()
	if err := s.conn.Close(cws.StatusNormalClosure, "bye"); err != nil {
		s.t.Fatal(err)
	}
}

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

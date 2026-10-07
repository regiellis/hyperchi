package hyperchi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type chatLine struct {
	Text string `form:"text" validate:"required,max=20"`
}

type wireMessage struct {
	Type    string `json:"type"`
	Event   string `json:"event"`
	Content string `json:"content"`
	Target  string `json:"target"`
	Swap    string `json:"swap"`
	Data    any    `json:"data"`
}

func dialSocket(t *testing.T, ctx context.Context, srv *httptest.Server, path string) *cws.Conn {
	t.Helper()
	conn, resp, err := cws.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, nil)
	if resp != nil && resp.Body != nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close handshake body: %v", cerr)
		}
	}
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := conn.CloseNow(); err != nil {
			t.Logf("close after test: %v", err)
		}
	})
	return conn
}

// readWire returns the next message the test cares about, skipping the hub's
// room_join and room_leave notices.
func readWire(t *testing.T, ctx context.Context, conn *cws.Conn) wireMessage {
	t.Helper()
	for {
		var m wireMessage
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.Type != "room_join" && m.Type != "room_leave" {
			return m
		}
	}
}

func socketApp(t *testing.T) (*HyperChi, *httptest.Server) {
	t.Helper()
	app := newTemplateApp(t, false)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, srv
}

func TestSocketReceivesHTMXMessagesAndSwaps(t *testing.T) {
	app, srv := socketApp(t)
	ended := make(chan error, 1)
	app.WebSocket("/chat/{room}", func(s *Socket) error {
		err := func() error {
			if got := s.Context().Param("room"); got != "lobby" {
				return errors.New("room param = " + got)
			}
			for {
				msg, err := s.Receive()
				if err != nil {
					return err
				}
				line, err := BindMessage[chatLine](msg)
				if fields := FieldErrors(err); fields != nil {
					if err := s.SendJSON("invalid", fields); err != nil {
						return err
					}
					continue
				} else if err != nil {
					return err
				}
				if msg.Header("hx-source") != "form#chat" {
					return errors.New("missing HX-Source header: " + msg.Header("HX-Source"))
				}
				if err := s.Swap("#feed", "beforeend", "page", H{"msg": line.Text}); err != nil {
					return err
				}
			}
		}()
		ended <- err
		return err
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialSocket(t, ctx, srv, "/chat/lobby")

	// The htmx 4 ws extension sends form values plus a "headers" object.
	send := func(v any) {
		if err := wsjson.Write(ctx, conn, v); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"text": "hello <b>", "headers": map[string]string{"HX-Request": "true", "HX-Source": "form#chat"}})
	m := readWire(t, ctx, conn)
	if m.Type != "htmx" || m.Target != "#feed" || m.Swap != "beforeend" || m.Content != "<p>hello &lt;b&gt;</p>" {
		t.Fatalf("swap message = %+v", m)
	}

	send(map[string]any{"text": "", "headers": map[string]string{"HX-Source": "form#chat"}})
	m = readWire(t, ctx, conn)
	fields, ok := m.Data.(map[string]any)
	if m.Type != "json" || m.Event != "invalid" || !ok || fields["text"] == nil {
		t.Fatalf("validation message = %+v", m)
	}

	if err := conn.Close(cws.StatusNormalClosure, "bye"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if !errors.Is(err, ErrSocketClosed) {
			t.Fatalf("handler ended with %v, want ErrSocketClosed", err)
		}
	case <-ctx.Done():
		t.Fatal("handler did not see the client close")
	}
}

func TestRoomBroadcastReachesJoinedSockets(t *testing.T) {
	app, srv := socketApp(t)
	joined := make(chan struct{}, 2)
	app.WebSocket("/live", func(s *Socket) error {
		s.Join("news")
		joined <- struct{}{}
		return s.Wait()
	})
	app.Post("/publish", func(c *Context) error {
		if err := c.App().Room("news").Swap("#ticker", "innerHTML", "page", H{"msg": c.FormValue("msg")}); err != nil {
			return err
		}
		return c.NoContent()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := dialSocket(t, ctx, srv, "/live")
	b := dialSocket(t, ctx, srv, "/live")
	for i := 0; i < 2; i++ {
		select {
		case <-joined:
		case <-ctx.Done():
			t.Fatal("sockets did not join")
		}
	}

	resp, err := http.Post(srv.URL+"/publish", "application/x-www-form-urlencoded", strings.NewReader("msg=breaking"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("publish status %d", resp.StatusCode)
	}
	for name, conn := range map[string]*cws.Conn{"a": a, "b": b} {
		m := readWire(t, ctx, conn)
		if m.Target != "#ticker" || m.Content != "<p>breaking</p>" {
			t.Fatalf("%s got %+v", name, m)
		}
	}
}

func TestSocketHandlerErrorClosesWithInternalError(t *testing.T) {
	app, srv := socketApp(t)
	app.WebSocket("/fail", func(s *Socket) error {
		if _, err := s.Receive(); err != nil {
			return err
		}
		return errors.New("boom")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialSocket(t, ctx, srv, "/fail")
	if err := conn.Write(ctx, cws.MessageText, []byte("go")); err != nil {
		t.Fatal(err)
	}
	_, _, err := conn.Read(ctx)
	if got := cws.CloseStatus(err); got != cws.StatusInternalError {
		t.Fatalf("close status = %v (%v), want 1011", got, err)
	}
}

func TestSocketRouteRejectsPlainHTTP(t *testing.T) {
	app, srv := socketApp(t)
	app.WebSocket("/ws", func(s *Socket) error { return s.Wait() })
	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode < 400 {
		t.Fatalf("plain GET to a socket route answered %d", resp.StatusCode)
	}
}

func TestSocketMessageValues(t *testing.T) {
	m := parseSocketMessage([]byte(`{"tag":["a","b"],"n":3,"headers":{"hx-current-url":"/x"}}`))
	if m.Value("tag") != "a" || m.Value("n") != "3" || m.Header("HX-Current-URL") != "/x" || m.Values()["headers"] != nil {
		t.Fatalf("parsed %+v", m)
	}
	raw := parseSocketMessage([]byte("plain text"))
	if raw.Text() != "plain text" || raw.Values() != nil {
		t.Fatalf("raw %+v", raw)
	}
	var line chatLine
	if err := raw.Bind(&line); err == nil {
		t.Fatal("Bind of a non-object message succeeded")
	}
}

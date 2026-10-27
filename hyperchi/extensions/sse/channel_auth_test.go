package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// With an authorizer installed, a stream asking for a refused channel is
// answered 403 before it opens, and an allowed one subscribes.
func TestChannelAuthorizer(t *testing.T) {
	hub := NewSSEHub()
	hub.SetChannelAuthorizer(func(r *http.Request, channel string) bool {
		return channel == "public" || r.Header.Get("X-Test-User") == channel
	})

	serve := func(query, user string) *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the stream returns as soon as it is set up
		r := httptest.NewRequest(http.MethodGet, "/sse?channels="+query, nil).WithContext(ctx)
		if user != "" {
			r.Header.Set("X-Test-User", user)
		}
		w := httptest.NewRecorder()
		hub.HTTPHandler()(w, r)
		return w
	}

	if w := serve("public,private", ""); w.Code != http.StatusForbidden {
		t.Errorf("refused channel: status %d, want 403", w.Code)
	}
	if hub.GetStats().TotalConnections != 0 {
		t.Error("a refused stream was registered")
	}
	if w := serve("public,alice", "alice"); w.Code != http.StatusOK {
		t.Errorf("allowed channels: status %d", w.Code)
	}
	if hub.GetStats().TotalConnections != 1 {
		t.Error("an allowed stream was not registered")
	}
}

// Without an authorizer every requested channel is allowed, as before.
func TestNoChannelAuthorizerAllows(t *testing.T) {
	hub := NewSSEHub()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	hub.HTTPHandler()(w, httptest.NewRequest(http.MethodGet, "/sse?channels=anything", nil).WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
}

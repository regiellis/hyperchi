package helpers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestJSONDefaultsAndNilObjects(t *testing.T) {
	for _, value := range []any{nil, "bad", true, []int{1}} {
		j := JSON{"value": value}
		if j.GetIntOr("value", 7) != 7 || j.GetFloatOr("value", 7) != 7 {
			t.Fatalf("invalid value %v ignored fallback", value)
		}
	}
	for _, value := range []any{0, float64(0), "0"} {
		j := JSON{"value": value}
		if j.GetIntOr("value", 7) != 0 || j.GetFloatOr("value", 7) != 0 {
			t.Fatal("zero replaced with fallback")
		}
	}
	j, err := FromJSON("null")
	if err != nil || j == nil {
		t.Fatalf("null: %v, %v", j, err)
	}
	j = j.Set("child", NewJSON().Set("count", 3))
	if j.GetJSON("child").GetInt("count") != 3 {
		t.Fatal("named nested object lost")
	}
	var empty JSON
	empty = empty.Set("a", 1).Merge(JSON{"b": 2})
	if len(empty) != 2 {
		t.Fatal(empty)
	}
}

func TestOrEmptyJSON(t *testing.T) {
	s := NewSlice(1, 2).Filter(func(int) bool { return false })
	encoded, err := json.Marshal(s.OrEmpty())
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("%s %v", encoded, err)
	}
	original := NewSlice(1)
	original.OrEmpty()[0] = 2
	if original[0] != 2 {
		t.Fatal("non-empty slice copied")
	}
}

func TestHTTPContextMethods(t *testing.T) {
	calls := []struct {
		name string
		call func(*HTTPClient, context.Context) (*HTTPResponse, error)
	}{
		{"GET", func(c *HTTPClient, ctx context.Context) (*HTTPResponse, error) { return c.GetWithContext(ctx, "/") }},
		{"DELETE", func(c *HTTPClient, ctx context.Context) (*HTTPResponse, error) { return c.DeleteWithContext(ctx, "/") }},
		{"POST", func(c *HTTPClient, ctx context.Context) (*HTTPResponse, error) {
			return c.PostWithContext(ctx, "/", JSON{"ok": true})
		}},
		{"PUT", func(c *HTTPClient, ctx context.Context) (*HTTPResponse, error) {
			return c.PutWithContext(ctx, "/", "raw")
		}},
		{"PATCH", func(c *HTTPClient, ctx context.Context) (*HTTPResponse, error) {
			return c.PatchWithContext(ctx, "/", JSON{"ok": true})
		}},
		{"form", func(c *HTTPClient, ctx context.Context) (*HTTPResponse, error) {
			return c.PostFormWithContext(ctx, "/", url.Values{"q": {"a b"}})
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.name == "form" {
					if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
						t.Error("wrong form content type")
					}
					if err := r.ParseForm(); err != nil || r.Form.Get("q") != "a b" {
						t.Error("wrong form data")
					}
				} else if r.Method != tc.name {
					t.Errorf("method %s", r.Method)
				}
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()
			client := NewHTTPClient().WithBaseURL(srv.URL).WithJSONHeaders()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if resp, err := tc.call(client, ctx); !errors.Is(err, context.Canceled) || resp != nil {
				t.Fatalf("cancel: %v %v", resp, err)
			}
			resp, err := tc.call(client, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var result JSON
			if err = resp.Parse(&result); err != nil || !result.GetBool("ok") {
				t.Fatalf("parse: %v %v", result, err)
			}
			if client.headers["Content-Type"] != "application/json" {
				t.Fatal("form modified client headers")
			}
		})
	}
}

func TestEmptyResponseBody(t *testing.T) {
	for _, resp := range []*HTTPResponse{nil, {}, {resp: &http.Response{}}} {
		if _, err := resp.Text(); err == nil {
			t.Fatal("missing body accepted")
		}
		var v any
		if err := resp.Parse(&v); err == nil {
			t.Fatal("missing body parsed")
		}
		if err := resp.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPInFlightCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		resp, err := NewHTTPClient().GetWithContext(ctx, srv.URL)
		if resp != nil {
			_ = resp.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
}

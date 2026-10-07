package pocketbase

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecordMarshalRoundTrip(t *testing.T) {
	original := Record{
		ID:             "one",
		CollectionID:   "posts-id",
		CollectionName: "posts",
		Created:        "created",
		Updated:        "updated",
		Data:           map[string]any{"title": "Hello", "amount": float64(42), "tags": []any{"go"}},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Record
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip changed the record:\n got %+v\nwant %+v\nJSON %s", decoded, original, encoded)
	}

	// A record inside another value encodes the same way.
	wrapped, err := json.Marshal(map[string]any{"record": original, "records": []Record{original}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(wrapped), `"title":"Hello"`) != 2 {
		t.Fatalf("nested records lost their fields: %s", wrapped)
	}
}

// pathRecorder answers every request with a record and remembers the raw
// path and query it was sent.
type pathRecorder struct {
	server *httptest.Server
	paths  []string
}

func newPathRecorder(t *testing.T) *pathRecorder {
	t.Helper()
	rec := &pathRecorder{}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.paths = append(rec.paths, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if _, err := fmt.Fprint(w, `{"id":"x","items":[],"token":"t"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

func TestRecordPathsEscapeHostileSegments(t *testing.T) {
	for _, id := range []string{"../users", "a/b?x=1", "a#b"} {
		t.Run(id, func(t *testing.T) {
			rec := newPathRecorder(t)
			col := New(Config{BaseURL: rec.server.URL}).Client().Collection("posts")
			if _, err := col.GetOne(id); err != nil {
				t.Fatal(err)
			}
			if _, err := col.Update(id, map[string]any{"title": "x"}); err != nil {
				t.Fatal(err)
			}
			if err := col.Delete(id); err != nil {
				t.Fatal(err)
			}
			want := "/api/collections/posts/records/" + strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23").Replace(id) + "?"
			for i, method := range []string{"GET", "PATCH", "DELETE"} {
				if got := rec.paths[i]; got != method+" "+want {
					t.Errorf("%s sent %q, want %q", method, got, method+" "+want)
				}
			}
		})
	}
}

func TestCollectionNamesAreEscaped(t *testing.T) {
	rec := newPathRecorder(t)
	client := New(Config{BaseURL: rec.server.URL}).Client()
	col := client.Collection("../_superusers")
	if _, err := col.Create(map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := col.Filter("a = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := col.GetFullList(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UserAuth("users/x", "me", "pw"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /api/collections/..%2F_superusers/records?",
		"GET /api/collections/..%2F_superusers/records?filter=a+%3D+1",
		"GET /api/collections/..%2F_superusers/records?page=1&perPage=500",
		"POST /api/collections/users%2Fx/auth-with-password?",
	}
	if !reflect.DeepEqual(rec.paths, want) {
		t.Fatalf("paths:\n got %q\nwant %q", rec.paths, want)
	}
}

func TestDotSegmentsAreRejected(t *testing.T) {
	rec := newPathRecorder(t)
	client := New(Config{BaseURL: rec.server.URL}).Client()
	for _, id := range []string{"", ".", ".."} {
		if _, err := client.Collection("posts").GetOne(id); err == nil {
			t.Errorf("GetOne(%q) succeeded", id)
		}
		if err := client.Collection("posts").Delete(id); err == nil {
			t.Errorf("Delete(%q) succeeded", id)
		}
		if _, err := client.Collection(id).Create(nil); err == nil {
			t.Errorf("Collection(%q).Create succeeded", id)
		}
	}
	if len(rec.paths) != 0 {
		t.Fatalf("requests were sent: %q", rec.paths)
	}
}

// flakyServer answers 503 to the first failures requests, then 200.
func flakyServer(t *testing.T, failures int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= failures {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		if _, err := fmt.Fprint(w, `{"id":"one"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestGetRetriesTransientFailures(t *testing.T) {
	server, requests := flakyServer(t, 2)
	col := New(Config{BaseURL: server.URL, RetryAttempts: 2, RetryDelay: time.Microsecond}).Client().Collection("posts")
	record, err := col.GetOne("one")
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "one" || requests.Load() != 3 {
		t.Fatalf("record %+v after %d requests", record, requests.Load())
	}
}

func TestGetGivesUpAfterRetryAttempts(t *testing.T) {
	server, requests := flakyServer(t, 100)
	col := New(Config{BaseURL: server.URL, RetryAttempts: 1, RetryDelay: time.Microsecond}).Client().Collection("posts")
	if _, err := col.GetOne("one"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %v, want the last 503", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestWritesAreNotRetried(t *testing.T) {
	server, requests := flakyServer(t, 100)
	col := New(Config{BaseURL: server.URL, RetryAttempts: 3, RetryDelay: time.Microsecond}).Client().Collection("posts")
	if _, err := col.Create(map[string]any{"a": 1}); err == nil {
		t.Fatal("create succeeded against a failing server")
	}
	if _, err := col.Update("one", map[string]any{"a": 1}); err == nil {
		t.Fatal("update succeeded against a failing server")
	}
	if err := col.Delete("one"); err == nil {
		t.Fatal("delete succeeded against a failing server")
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want one per write", requests.Load())
	}
}

func TestZeroRetryAttemptsSendsOnce(t *testing.T) {
	server, requests := flakyServer(t, 100)
	if _, err := New(Config{BaseURL: server.URL}).Client().Collection("posts").GetOne("one"); err == nil {
		t.Fatal("expected the 503")
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestErrorBodyReadFailureIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := fmt.Fprint(w, "short"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	_, err := New(Config{BaseURL: server.URL}).Client().Collection("posts").GetOne("one")
	if err == nil || !strings.Contains(err.Error(), "reading the body") {
		t.Fatalf("error = %v, want the body read failure", err)
	}
}

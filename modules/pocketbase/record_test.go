package pocketbase

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRecordMethodsPreserveCustomFields(t *testing.T) {
	const record = `{"id":"one","collectionId":"posts-id","collectionName":"posts","created":"created","updated":"updated","title":"Hello","amount":42,"published":true,"tags":["go"],"metadata":{"nested":"value"},"optional":null}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := record
		if r.Method == "GET" && r.URL.Path == "/api/collections/posts/records" {
			body = `{"items":[` + record + `]}`
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	col := New(Config{BaseURL: server.URL}).Client().Collection("posts")
	methods := map[string]func() (*Record, error){"get": func() (*Record, error) { return col.GetOne("one") }, "create": func() (*Record, error) { return col.Create(map[string]any{"title": "Hello"}) }, "update": func() (*Record, error) { return col.Update("one", map[string]any{"title": "Hello"}) }, "list": func() (*Record, error) {
		rs, err := col.GetFullList()
		if err != nil {
			return nil, err
		}
		return &rs[0], nil
	}, "filter": func() (*Record, error) {
		rs, err := col.Filter("published=true")
		if err != nil {
			return nil, err
		}
		return &rs[0], nil
	}}
	want := map[string]any{"title": "Hello", "amount": float64(42), "published": true, "tags": []any{"go"}, "metadata": map[string]any{"nested": "value"}, "optional": nil}
	for name, call := range methods {
		t.Run(name, func(t *testing.T) {
			r, err := call()
			if err != nil {
				t.Fatal(err)
			}
			if r.ID != "one" || r.CollectionID != "posts-id" || r.CollectionName != "posts" || r.Created != "created" || r.Updated != "updated" {
				t.Fatalf("metadata lost: %+v", r)
			}
			if !reflect.DeepEqual(r.Data, want) {
				t.Fatalf("Data=%#v want %#v", r.Data, want)
			}
		})
	}
}

func TestRecordDecodeReplacesCustomFields(t *testing.T) {
	var r Record
	if err := json.Unmarshal([]byte(`{"id":"one","title":"old"}`), &r); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":"two","active":true}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.ID != "two" || !reflect.DeepEqual(r.Data, map[string]any{"active": true}) {
		t.Fatalf("stale record fields: %+v", r)
	}
	if err := json.Unmarshal([]byte(`{"id":42}`), &r); err == nil {
		t.Fatal("invalid metadata accepted")
	}
}

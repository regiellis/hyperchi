package pocketbase

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminAuthUsesSuperusers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/collections/_superusers/auth-with-password":
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if r.Method != http.MethodPost || input["identity"] != "admin@example.com" || input["password"] != "test-password" {
				t.Error("invalid authentication request")
			}
			if err := json.NewEncoder(w).Encode(AuthResponse{Token: "superuser-token"}); err != nil {
				t.Error(err)
			}
		case "/api/collections/posts/records/one":
			if r.Header.Get("Authorization") != "superuser-token" {
				t.Error("missing superuser token")
			}
			if err := json.NewEncoder(w).Encode(map[string]string{"id": "one"}); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL}).Client()
	if err := client.AdminAuth("admin@example.com", "test-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Collection("posts").GetOne("one"); err != nil {
		t.Fatal(err)
	}
}

func TestGetFullListFetchesEveryPage(t *testing.T) {
	for _, failSecond := range []bool{false, true} {
		name := "success"
		if failSecond {
			name = "page_error"
		}
		t.Run(name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				page := r.URL.Query().Get("page")
				if page == "2" && failSecond {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				id := "one"
				if page == "2" {
					id = "two"
				}
				if err := json.NewEncoder(w).Encode(ListResponse{TotalPages: 2, Items: []Record{{ID: id}}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			records, err := New(Config{BaseURL: server.URL}).Client().Collection("posts").GetFullList()
			if failSecond {
				if err == nil || records != nil {
					t.Fatalf("expected page failure, got %v, %v", records, err)
				}
			} else if err != nil || len(records) != 2 || records[0].ID != "one" || records[1].ID != "two" {
				t.Fatalf("records = %v, %v", records, err)
			}
			if requests != 2 {
				t.Errorf("requests = %d, want 2", requests)
			}
		})
	}
}

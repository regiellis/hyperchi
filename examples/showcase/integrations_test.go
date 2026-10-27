package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

func TestMarketingRoutes(t *testing.T) {
	cfg := config.Production()
	cfg.Database.Name = filepath.Join(t.TempDir(), "marketing.db")
	app := hyperchi.NewWithConfig(cfg)
	t.Cleanup(func() { _ = app.CloseDB() })
	if err := app.LoadTemplates("templates/*.tmpl"); err != nil {
		t.Fatal(err)
	}
	if err := setupIntegrations(app); err != nil {
		t.Fatal(err)
	}
	if err := app.Integrations().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Integrations().Close(context.Background()) })
	for _, tt := range []struct {
		name, path, contains string
		status               int
	}{
		{"templ page", "/examples/templ", "A clearer", 200},
		{"templ identity", "/examples/templ/estimate?scope=identity", "$4,000", 200},
		{"templ complete fallback", "/examples/templ?scope=complete", "$9,000", 200},
		{"jet page", "/examples/jet", "Small team", 200},
		{"jet annual", "/examples/jet/pricing?billing=annual", "$32.50", 200},
		{"jet annual fallback", "/examples/jet?billing=annual", "$180 or $390", 200},
		{"alpine page", "/examples/alpine", "The high trail", 200},
		{"invalid scope", "/examples/templ/estimate?scope=unknown", "Unknown", 400},
		{"invalid billing", "/examples/jet/pricing?billing=forever", "Unknown", 400},
		{"invalid terrain", "/examples/alpine/trips?terrain=unknown", "Unknown", 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			r.Header.Set("Accept", "application/json")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.contains) {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tt.status == 200 && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("content type %s", w.Header().Get("Content-Type"))
			}
			if strings.Contains(tt.path, "/estimate") || strings.Contains(tt.path, "/pricing") {
				if strings.Contains(w.Body.String(), "<html") {
					t.Fatal("fragment unexpectedly contains a document")
				}
			}
		})
	}
	for _, terrain := range []string{"all", "coast", "mountain", "forest"} {
		t.Run("json "+terrain, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", "/examples/alpine/trips?terrain="+terrain, nil))
			var data struct {
				Trips []trip `json:"trips"`
				Count int    `json:"count"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			expected := map[string]int{"all": 4, "coast": 1, "mountain": 2, "forest": 1}[terrain]
			if w.Code != 200 || data.Count != expected || len(data.Trips) != expected {
				t.Fatalf("invalid trips response: %s", w.Body.String())
			}
			for _, tr := range data.Trips {
				if terrain != "all" && tr.Terrain != terrain {
					t.Errorf("unfiltered trip: %+v", tr)
				}
			}
		})
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("POST", "/examples/alpine/trips", nil))
	if w.Code != 405 {
		t.Fatalf("POST status %d", w.Code)
	}
}

func TestEstimateEscapesText(t *testing.T) {
	var b bytes.Buffer
	if err := estimateCard(studioEstimate{Scope: `<script>alert(1)</script>`}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "<script>") || !strings.Contains(b.String(), "&lt;script&gt;") {
		t.Fatalf("unsafe estimate: %s", b.String())
	}
}

func TestTripCatalogEmptyAndInvalidResponses(t *testing.T) {
	app := inputTestApp(t)
	registerTrips(app, "/empty-trips", nil)
	for _, tt := range []struct {
		name, path string
		status     int
		body       string
	}{
		{"empty collection", "/empty-trips?terrain=coast", 200, `{"count":0,"trips":[]}`},
		{"invalid terrain", "/empty-trips?terrain=desert", 400, `{"error":"Unknown terrain"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != tt.status || strings.TrimSpace(w.Body.String()) != tt.body {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("content type %s", w.Header().Get("Content-Type"))
			}
		})
	}
}

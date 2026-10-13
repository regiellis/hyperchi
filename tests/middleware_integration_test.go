package tests

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

func TestHTMXMiddlewareFeatures(t *testing.T) {
	app := hyperchi.New()

	app.Use(app.Middleware.HTMX())

	app.Get("/test", func(c *hyperchi.Context) error {
		if c.IsHTMX() {
			return c.Text("HTMX request detected")
		}
		return c.Text("Regular request")
	})

	t.Run("HTMXRequest", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "HTMX request detected") {
			t.Error("HTMX middleware should detect HTMX request")
		}
	})

	t.Run("RegularRequest", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if !strings.Contains(rec.Body.String(), "Regular request") {
			t.Error("Should be treated as regular request")
		}
	})
}

func TestCSRFMiddleware(t *testing.T) {
	app := hyperchi.New()

	app.Use(app.Middleware.CSRF())

	app.Get("/form", func(c *hyperchi.Context) error {
		if token := c.CSRFToken(); token != "" {
			return c.JSON(hyperchi.H{"csrf_token": token})
		}
		return c.JSON(hyperchi.H{"error": "no token"})
	})

	app.Post("/submit", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"status": "success"})
	})

	t.Run("GetCSRFToken", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/form", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "csrf_token") {
			t.Error("Should receive CSRF token")
		}
	})

	t.Run("PostWithoutToken", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/submit", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", rec.Code)
		}
	})
}

func TestRateLimiterMiddleware(t *testing.T) {
	app := hyperchi.New()

	// A hand-written limiter: 5 requests per minute per RemoteAddr. The test
	// pins that app.Use middleware can answer before the handler runs.
	requestCounts := make(map[string]int)
	resetTime := time.Now()

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr

			if time.Since(resetTime) > time.Minute {
				requestCounts = make(map[string]int)
				resetTime = time.Now()
			}

			requestCounts[ip]++
			if requestCounts[ip] > 5 {
				w.WriteHeader(http.StatusTooManyRequests)
				if err := hyperchi.WriteJSON(w, hyperchi.H{"error": "rate limit exceeded"}); err != nil {
					t.Errorf("WriteJSON failed: %v", err)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	app.Get("/api/data", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"data": "test"})
	})

	t.Run("RateLimiting", func(t *testing.T) {
		// Make 6 requests (limit is 5)
		for i := 0; i < 6; i++ {
			req := httptest.NewRequest("GET", "/api/data", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			if i < 5 {
				if rec.Code != http.StatusOK {
					t.Errorf("Request %d: expected 200, got %d", i+1, rec.Code)
				}
			} else {
				if rec.Code != http.StatusTooManyRequests {
					t.Errorf("Request %d: expected 429, got %d", i+1, rec.Code)
				}
			}
		}
	})
}

func TestMiddlewareChaining(t *testing.T) {
	app := hyperchi.New()

	var executionOrder []string

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "before-1")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "after-1")
		})
	})

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "before-2")
			next.ServeHTTP(w, r)
			executionOrder = append(executionOrder, "after-2")
		})
	})

	app.Get("/chain", func(c *hyperchi.Context) error {
		executionOrder = append(executionOrder, "handler")
		return c.JSON(hyperchi.H{"status": "ok"})
	})

	t.Run("MiddlewareOrder", func(t *testing.T) {
		executionOrder = []string{}

		req := httptest.NewRequest("GET", "/chain", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		expected := []string{"before-1", "before-2", "handler", "after-2", "after-1"}

		if len(executionOrder) != len(expected) {
			t.Fatalf("Expected %d executions, got %d", len(expected), len(executionOrder))
		}

		for i, v := range expected {
			if executionOrder[i] != v {
				t.Errorf("Position %d: expected %s, got %s", i, v, executionOrder[i])
			}
		}
	})
}

func TestRequestIDMiddleware(t *testing.T) {
	app := hyperchi.New()

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = generateRequestID()
			}

			w.Header().Set("X-Request-ID", requestID)
			next.ServeHTTP(w, r)
		})
	})

	app.Get("/", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"status": "ok"})
	})

	t.Run("RequestIDGeneration", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		requestID := rec.Header().Get("X-Request-ID")
		if requestID == "" {
			t.Error("Request ID should be generated")
		}
	})

	t.Run("RequestIDPreservation", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Request-ID", "test-123")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		requestID := rec.Header().Get("X-Request-ID")
		if requestID != "test-123" {
			t.Error("Request ID should be preserved")
		}
	})
}

func TestCORSMiddleware(t *testing.T) {
	app := hyperchi.New()

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	})

	app.Get("/api/data", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"data": "test"})
	})

	t.Run("CORSHeaders", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/data", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("CORS origin header not set")
		}
	})

	t.Run("CORSPreflight", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/api/data", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("Expected status 204, got %d", rec.Code)
		}

		if rec.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Error("CORS methods header not set")
		}
	})
}

// generateRequestID returns a timestamp-based ID; uniqueness does not matter here.
func generateRequestID() string {
	return "req-" + time.Now().Format("20060102150405")
}

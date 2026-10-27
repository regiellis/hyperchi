package sse

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestSSEOriginEnvironmentPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, environment, goEnv, origin, want string }{
		{"production environment", "production", "", "", ""},
		{"environment wins", "production", "development", "", ""},
		{"go environment fallback", "", "production", "", ""},
		{"development default", "", "", "", "*"},
		{"explicit origin", "production", "", "https://example.com", "https://example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ENVIRONMENT", tc.environment)
			t.Setenv("GO_ENV", tc.goEnv)
			t.Setenv("CORS_ORIGINS", tc.origin)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			req := httptest.NewRequest("GET", "/sse", nil).WithContext(ctx)
			response := httptest.NewRecorder()
			NewSSEHub().HTTPHandler().ServeHTTP(response, req)
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
				t.Fatalf("origin=%q want %q", got, tc.want)
			}
		})
	}
}

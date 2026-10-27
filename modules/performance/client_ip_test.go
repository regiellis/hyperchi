package performance

import (
	"net/http/httptest"
	"testing"
)

func TestRateLimiterIgnoresForwardingHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.7:4242"
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	r.Header.Set("X-Real-IP", "6.6.6.6")
	if got := (&RateLimiter{}).getClientIP(r); got != "198.51.100.7" {
		t.Fatalf("client IP = %q, want the TCP peer", got)
	}
}

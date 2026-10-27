package hyperchi

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Behind a trusted proxy the limiter keys on the first untrusted address from
// the right of X-Forwarded-For; entries to its left came from the client.
func TestRateLimiterKeyWalksForwardedForFromTheRight(t *testing.T) {
	l, err := NewRateLimiterE(&RateLimitConfig{
		RequestsPerWindow: 1, WindowDuration: time.Minute, TrustedProxies: []string{"10.0.0.0/8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ peer, xff, want string }{
		{"10.0.0.5:1000", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"10.0.0.5:1000", "6.6.6.6, 203.0.113.9, 10.0.0.7", "203.0.113.9"},
		{"198.51.100.7:1000", "6.6.6.6, 203.0.113.9", "198.51.100.7"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		if got := l.key(r); got != tc.want {
			t.Errorf("peer %s xff %q: key %q, want %q", tc.peer, tc.xff, got, tc.want)
		}
	}
}

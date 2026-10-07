package security

import (
	"net/http/httptest"
	"testing"
)

func TestGetClientIPWalksForwardedForFromTheRight(t *testing.T) {
	cases := []struct{ peer, xff, want string }{
		{"127.0.0.1:9000", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"127.0.0.1:9000", "203.0.113.9, 127.0.0.1", "203.0.113.9"},
		{"198.51.100.7:9000", "6.6.6.6", "198.51.100.7"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		if got := getClientIP(r); got != tc.want {
			t.Errorf("peer %s xff %q: %q, want %q", tc.peer, tc.xff, got, tc.want)
		}
	}
}

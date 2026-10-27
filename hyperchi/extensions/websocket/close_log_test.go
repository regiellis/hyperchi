package websocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/coder/websocket"
)

func TestTeardownCloseErrorIsExpected(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"already closed", fmt.Errorf("failed to immediately close WebSocket: %w", net.ErrClosed), true},
		{"peer gone", fmt.Errorf("failed to close WebSocket: failed to read frame header: %w", io.EOF), true},
		{"unexpected", errors.New("failed to close WebSocket: tls: bad record MAC"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expectedCloseErr(tc.err); got != tc.want {
				t.Fatalf("expectedCloseErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// A page closing mid-write is routine; only unexpected write failures are worth
// a log line.
func TestWriteErrorFromDisconnectIsExpected(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"closed conn", fmt.Errorf("failed to write msg: %w", net.ErrClosed), true},
		{"connection context cancelled", fmt.Errorf("failed to write: %w", context.Canceled), true},
		{"peer sent close", fmt.Errorf("failed to write: %w", websocket.CloseError{Code: websocket.StatusGoingAway}), true},
		{"timeout", fmt.Errorf("failed to write: %w", context.DeadlineExceeded), false},
		{"unexpected", errors.New("json: unsupported type"), false},
	}
	for _, tc := range cases {
		if got := expectedDisconnectErr(tc.err); got != tc.want {
			t.Errorf("%s: expectedDisconnectErr = %v, want %v", tc.name, got, tc.want)
		}
	}
}

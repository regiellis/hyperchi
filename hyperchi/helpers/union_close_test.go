package helpers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Union must not write into the spare capacity of the receiver's backing
// array, which another slice may share.
func TestUnionLeavesReceiverBackingArrayAlone(t *testing.T) {
	backing := []int{1, 2, 99, 100}
	s := Slice[int](backing[:2])
	got := s.Union(Slice[int]{3, 4})
	if backing[2] != 99 || backing[3] != 100 {
		t.Fatalf("Union overwrote the receiver's backing array: %v", backing)
	}
	if want := []int{1, 2, 3, 4}; !equalInts(got, want) {
		t.Fatalf("Union = %v, want %v", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var errBodyClose = errors.New("close failed")

// closeFailBody reads its content, then fails to close.
type closeFailBody struct{ io.Reader }

func (closeFailBody) Close() error { return errBodyClose }

func closeFailResponse(body string) *HTTPResponse {
	return &HTTPResponse{resp: &http.Response{StatusCode: http.StatusOK, Body: closeFailBody{strings.NewReader(body)}}}
}

// Text and Parse report a body that fails to close instead of discarding it.
func TestResponseReadersReportCloseErrors(t *testing.T) {
	if _, err := closeFailResponse("hello").Text(); !errors.Is(err, errBodyClose) {
		t.Errorf("Text error = %v, want %v", err, errBodyClose)
	}
	var v map[string]any
	if err := closeFailResponse(`{"a":1}`).Parse(&v); !errors.Is(err, errBodyClose) {
		t.Errorf("Parse error = %v, want %v", err, errBodyClose)
	}
}

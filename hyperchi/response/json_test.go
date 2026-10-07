package response

import (
	"net/http/httptest"
	"testing"
)

func TestJSONEncodingBeforeStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	response := New(rec).Status(201)
	if err := response.WriteJSON(make(chan int)); err == nil {
		t.Fatal("expected encoding error")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatal("encoding error committed response")
	}
	// A different status must still be writable after the failed encoding.
	rec.WriteHeader(422)
	if rec.Code != 422 {
		t.Fatalf("response committed status %d", rec.Code)
	}
}

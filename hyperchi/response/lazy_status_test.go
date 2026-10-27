package response

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// HX-* headers set after Status() must still be sent, so Status() cannot call
// WriteHeader itself. These tests read Result().Header, the snapshot taken at
// WriteHeader time. A bare recorder.Header() accepts late writes and would
// hide a status written too early.

func TestSuccessEmitsTriggerStatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	Success(w, "saved")

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if res.Header.Get("HX-Trigger") == "" {
		t.Fatal("HX-Trigger header was dropped (set after WriteHeader)")
	}
	if w.Body.String() != "saved" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "saved")
	}
}

func TestErrorEmitsTriggerAndStatus(t *testing.T) {
	w := httptest.NewRecorder()
	Error(w, http.StatusBadRequest, "nope")

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	if res.Header.Get("HX-Trigger") == "" {
		t.Fatal("HX-Trigger header was dropped on Error")
	}
}

func TestFluentStatusBeforeTriggerKeepsHeader(t *testing.T) {
	w := httptest.NewRecorder()
	if _, err := New(w).
		Status(http.StatusCreated).
		Trigger(map[string]any{"showToast": "hi"}).
		WriteString("ok"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	res := w.Result()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	if res.Header.Get("HX-Trigger") == "" {
		t.Fatal("HX-Trigger set after Status() must still be emitted")
	}
}

func TestNoContentEmitsStatusAndTrigger(t *testing.T) {
	w := httptest.NewRecorder()
	NoContent(w, map[string]any{"refresh": true})

	res := w.Result()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.StatusCode)
	}
	if res.Header.Get("HX-Trigger") == "" {
		t.Fatal("HX-Trigger header missing on NoContent")
	}
}

package security

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

func TestUploadRequiresAuthenticatedContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		token  string
		userID string
		public bool
		valid  bool
	}{
		{name: "missing identity"},
		{name: "fabricated bearer", token: strings.Repeat("A", 44)},
		{name: "trusted identity", userID: "user-1", valid: true},
		{name: "public upload", public: true, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreatePart(textproto.MIMEHeader{
				"Content-Disposition": {`form-data; name="file"; filename="test.txt"`},
				"Content-Type":        {"text/plain"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte("hello")); err != nil {
				t.Fatal(err)
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/upload", &body)
			r.Header.Set("Content-Type", form.FormDataContentType())
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.userID != "" {
				r = r.WithContext(WithUserID(r.Context(), tc.userID))
			}
			cfg := DefaultUploadConfig()
			cfg.RequireAuth = !tc.public
			w := httptest.NewRecorder()
			reached := false
			UploadMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				result := GetValidationResult(r)
				if result == nil || !result.Valid || len(result.Files) != 1 {
					t.Fatal("expected one validated file")
				}
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(w, r)
			if reached != tc.valid {
				t.Fatalf("handler reached = %v, want %v (status %d)", reached, tc.valid, w.Code)
			}
		})
	}
}

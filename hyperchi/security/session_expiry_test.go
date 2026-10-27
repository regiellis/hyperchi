package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// persistentSessionStore leaves expiry enforcement to the validator.
type persistentSessionStore struct{ records map[string]SessionRecord }

func (s *persistentSessionStore) Save(token, userID string, expiresAt time.Time) error {
	s.records[token] = SessionRecord{UserID: userID, ExpiresAt: expiresAt}
	return nil
}
func (s *persistentSessionStore) Lookup(token string) (SessionRecord, bool, error) {
	record, ok := s.records[token]
	return record, ok, nil
}
func (s *persistentSessionStore) Delete(token string) error { delete(s.records, token); return nil }

func TestCustomSessionStoreExpiry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expiry time.Duration
		status int
	}{
		{"expired", -time.Hour, http.StatusUnauthorized},
		{"active", time.Hour, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := NewAuthHelper()
			auth.SetSessionStore(&persistentSessionStore{records: make(map[string]SessionRecord)})
			auth.SetTokenExpiry(tc.expiry)
			token, err := auth.CreateLoginSession("user-1")
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, "/private", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			RequireAuth(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if GetUserIDFromContext(r.Context()) != "user-1" {
					t.Error("missing authenticated user")
				}
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
		})
	}
}

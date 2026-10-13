package hyperchi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionLogoutCannotResurrect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		modify    bool
		direct    bool
	}{
		{name: "fresh", remaining: 24 * time.Hour},
		{name: "sliding expiry", remaining: time.Hour},
		{name: "modified", remaining: 24 * time.Hour, modify: true},
		{name: "direct destroy", remaining: time.Hour, direct: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestSessionStore(t)
			session, err := store.GetSession(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			session.Set("user", "user-1")
			session.ExpiresAt = time.Now().Add(tc.remaining)
			if err := session.Save(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/logout", nil)
			request.AddCookie(&http.Cookie{Name: store.cookieName, Value: session.ID})
			recorder := httptest.NewRecorder()
			store.SessionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := GetSessionFromContext(r)
				if tc.modify {
					current.Set("updated", true)
				}
				var err error
				if tc.direct {
					err = current.Destroy()
				} else {
					err = store.DestroySession(w, r)
				}
				if err != nil {
					t.Fatal(err)
				}
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(recorder, request)
			if _, err := store.store.Get(store.keyPrefix + session.ID); !errors.Is(err, ErrKeyNotFound) {
				t.Fatalf("session still exists after logout: %v", err)
			}
			response := recorder.Result()
			defer func() {
				if err := response.Body.Close(); err != nil {
					t.Errorf("close response: %v", err)
				}
			}()
			cookies := response.Cookies()
			if len(cookies) == 0 {
				t.Fatal("logout did not clear session cookie")
			}
			for _, cookie := range cookies {
				if cookie.Name == store.cookieName && (cookie.MaxAge >= 0 || cookie.Value != "") {
					t.Fatalf("logout issued a live session cookie: %s", cookie)
				}
			}
		})
	}
}

type failingSessionDeleteStore struct {
	KVStore
	err error
}

func (s *failingSessionDeleteStore) Delete(key string) error { return s.err }

func TestSessionDestroyFailureKeepsSession(t *testing.T) {
	wantErr := errors.New("delete failed")
	backend := &failingSessionDeleteStore{KVStore: NewMemoryStore(), err: wantErr}
	session, err := NewKVSession(backend, "session:", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session.Set("user", "user-1")
	if err := session.Save(); err != nil {
		t.Fatal(err)
	}
	if err := session.Destroy(); !errors.Is(err, wantErr) {
		t.Fatalf("Destroy error = %v", err)
	}
	if session.GetString("user") != "user-1" {
		t.Fatal("failed destruction cleared session data")
	}
	session.Set("after", "still active")
	if err := session.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := backend.Get("session:" + session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "still active") {
		t.Fatal("failed destruction disabled subsequent persistence")
	}
}

func TestDestroyedSessionCannotBeSavedOrRegenerated(t *testing.T) {
	store := newTestSessionStore(t)
	session, err := store.GetSession(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	session.Set("user", "user-1")
	if err := session.Save(); err != nil {
		t.Fatal(err)
	}
	if err := session.Destroy(); err != nil {
		t.Fatal(err)
	}
	session.Set("user", "user-1")
	if err := session.Save(); err != nil {
		t.Fatal(err)
	}
	if err := session.Regenerate(); err == nil {
		t.Fatal("destroyed session regenerated")
	}
	if _, err := store.store.Get(store.keyPrefix + session.ID); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("destroyed session saved: %v", err)
	}
}

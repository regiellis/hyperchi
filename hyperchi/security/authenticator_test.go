package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type stubUserRepo struct{ user *User }

func (s stubUserRepo) FindByEmail(email string) (*User, error) {
	if s.user != nil && s.user.Email == email {
		return s.user, nil
	}
	return nil, nil
}

func (s stubUserRepo) UpdateLastLogin(int) error { return nil }

func testUser(t *testing.T) *User {
	t.Helper()
	// MinCost keeps the test fast. CheckPasswordHash reads the cost from the
	// hash.
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &User{ID: 7, Email: "a@example.com", PasswordHash: string(hash), Active: true}
}

// requireAuthAdmits sends token as a Bearer header through RequireAuth(v) and
// returns the user ID the handler saw, or "" when the request was refused.
func requireAuthAdmits(t *testing.T, v TokenValidator, token string) string {
	t.Helper()
	var seen string
	h := RequireAuth(v)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = GetUserIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		return ""
	}
	return seen
}

// The documented flow: one AuthHelper issues tokens through the
// Authenticator and validates them in RequireAuth.
func TestAuthenticatorTokensPassRequireAuth(t *testing.T) {
	helper := NewAuthHelper()
	auth := NewAuthenticator(stubUserRepo{user: testUser(t)}, helper)

	result := auth.AuthenticateUser(&LoginAttempt{Email: "a@example.com", Password: "correct horse"})
	if !result.Success {
		t.Fatalf("login failed: %s", result.Error)
	}
	if got := requireAuthAdmits(t, helper, result.Token); got != "7" {
		t.Fatalf("RequireAuth(helper) user = %q, want 7", got)
	}

	if err := helper.DestroyLoginSession(result.Token); err != nil {
		t.Fatal(err)
	}
	if got := requireAuthAdmits(t, helper, result.Token); got != "" {
		t.Fatal("a destroyed session still passed RequireAuth")
	}
}

func TestAuthenticatorWithoutHelperExposesItsOwn(t *testing.T) {
	auth := NewAuthenticator(stubUserRepo{user: testUser(t)}, nil)
	result := auth.AuthenticateUser(&LoginAttempt{Email: "a@example.com", Password: "correct horse"})
	if !result.Success {
		t.Fatalf("login failed: %s", result.Error)
	}
	if got := requireAuthAdmits(t, auth.Helper(), result.Token); got != "7" {
		t.Fatalf("RequireAuth(auth.Helper()) user = %q, want 7", got)
	}
}

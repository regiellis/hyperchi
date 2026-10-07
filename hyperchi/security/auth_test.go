package security

import (
	"strings"
	"testing"
	"time"
)

func TestHashPassword(t *testing.T) {
	password := "test-password-123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}

	if hash == password {
		t.Fatal("HashPassword returned the original password")
	}

	if !strings.HasPrefix(hash, "$2a$14$") && !strings.HasPrefix(hash, "$2b$14$") {
		t.Fatalf("Hash doesn't have expected bcrypt format: %s", hash)
	}
}

func TestCheckPasswordHash(t *testing.T) {
	password := "test-password-123"
	wrongPassword := "wrong-password"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !CheckPasswordHash(password, hash) {
		t.Fatal("CheckPasswordHash failed for correct password")
	}

	if CheckPasswordHash(wrongPassword, hash) {
		t.Fatal("CheckPasswordHash succeeded for wrong password")
	}
}

func TestCheckPasswordStrength(t *testing.T) {
	testCases := []struct {
		password string
		expected PasswordStrength
	}{
		{"123", StrengthWeak},
		{"password", StrengthWeak},
		{"Password1", StrengthMedium},
		{"Password123!", StrengthStrong},
		{"MyVerySecurePassword123!@#", StrengthStrong}, // This is actually strong, not very strong
		{"aaaaaaaaa", StrengthWeak},                    // Repetitive
		{"qwertyuiop", StrengthWeak},                   // Common pattern
	}

	for _, tc := range testCases {
		t.Run(tc.password, func(t *testing.T) {
			strength := CheckPasswordStrength(tc.password)
			if strength != tc.expected {
				t.Errorf("CheckPasswordStrength(%q) = %v, expected %v",
					tc.password, strength, tc.expected)
			}
		})
	}
}

func TestGenerateSecureToken(t *testing.T) {
	token1, err := GenerateSecureToken(32)
	if err != nil {
		t.Fatalf("GenerateSecureToken failed: %v", err)
	}

	token2, err := GenerateSecureToken(32)
	if err != nil {
		t.Fatalf("GenerateSecureToken failed: %v", err)
	}

	if token1 == token2 {
		t.Fatal("GenerateSecureToken returned identical tokens")
	}

	if len(token1) == 0 {
		t.Fatal("GenerateSecureToken returned empty token")
	}
}

func TestTimingSafeEqual(t *testing.T) {
	testString := "test-string"

	if !TimingSafeEqual(testString, testString) {
		t.Fatal("TimingSafeEqual failed for identical strings")
	}

	if TimingSafeEqual(testString, "different-string") {
		t.Fatal("TimingSafeEqual succeeded for different strings")
	}

	// Compare an early and a late mismatch of the same length. Wall-clock
	// timing is too noisy to assert on, so a large gap is only logged.
	start := time.Now()
	TimingSafeEqual("abcdefghijklmnop", "abcdefghijklmnXX")
	duration1 := time.Since(start)

	start = time.Now()
	TimingSafeEqual("abcdefghijklmnop", "Xbcdefghijklmnop")
	duration2 := time.Since(start)

	diff := duration1 - duration2
	if diff < 0 {
		diff = -diff
	}

	if diff > time.Microsecond*100 {
		t.Logf("Warning: Timing difference may be too large: %v", diff)
	}
}

func TestValidatePasswordRequirements(t *testing.T) {
	testCases := []struct {
		password    string
		shouldError bool
		description string
	}{
		{"123", true, "too short"},
		{"password", true, "too weak"},
		{"Password1", false, "meets requirements"},
		{"Password123!", false, "strong password"},
		{strings.Repeat("a", 129), true, "too long"},
		{"123456", true, "common password"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			err := ValidatePasswordRequirements(tc.password)
			if tc.shouldError && err == nil {
				t.Errorf("Expected error for password %q but got none", tc.password)
			}
			if !tc.shouldError && err != nil {
				t.Errorf("Unexpected error for password %q: %v", tc.password, err)
			}
		})
	}
}

func TestRehashPasswordIfNeeded(t *testing.T) {
	password := "test-password"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if RehashPasswordIfNeeded(hash) {
		t.Fatal("New hash shouldn't need rehashing")
	}

	if !RehashPasswordIfNeeded("invalid-hash") {
		t.Fatal("Invalid hash should need rehashing")
	}
}

func TestAuthHelper(t *testing.T) {
	helper := NewAuthHelper()

	token, err := helper.CreateLoginSession("user123")
	if err != nil {
		t.Fatalf("CreateLoginSession failed: %v", err)
	}

	if token == "" {
		t.Fatal("CreateLoginSession returned empty token")
	}

	helper.SetTokenExpiry(time.Hour * 2)
	if helper.tokenExpiry != time.Hour*2 {
		t.Fatal("SetTokenExpiry didn't update expiry time")
	}
}

// BenchmarkHashPassword measures one hash at cost 14. A login's
// CheckPasswordHash costs about the same.
func BenchmarkHashPassword(b *testing.B) {
	password := "benchmark-password-123"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := HashPassword(password)
		if err != nil {
			b.Fatalf("HashPassword failed: %v", err)
		}
	}
}

func BenchmarkCheckPasswordHash(b *testing.B) {
	password := "benchmark-password-123"
	hash, err := HashPassword(password)
	if err != nil {
		b.Fatalf("HashPassword failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !CheckPasswordHash(password, hash) {
			b.Fatal("CheckPasswordHash failed")
		}
	}
}

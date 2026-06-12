package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newTM(t *testing.T) *TokenManager {
	t.Helper()
	tm, err := NewTokenManager([]byte("jwt-secret"), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return tm
}

func TestNewTokenManagerValidation(t *testing.T) {
	if _, err := NewTokenManager(nil, time.Hour); err == nil {
		t.Error("expected error for empty secret")
	}
	if _, err := NewTokenManager([]byte("s"), 0); err == nil {
		t.Error("expected error for non-positive ttl")
	}
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	tm := newTM(t)
	token, err := tm.Issue("user-123")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	sub, err := tm.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if sub != "user-123" {
		t.Fatalf("subject = %q, want user-123", sub)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	issuer := newTM(t)
	token, _ := issuer.Issue("user-123")

	other, _ := NewTokenManager([]byte("different-secret"), time.Hour)
	if _, err := other.Verify(token); err == nil {
		t.Fatal("token verified under a different secret")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	tm := newTM(t)
	// Issue as if an hour and a half ago, so the 1h token is already expired.
	tm.now = func() time.Time { return time.Now().Add(-90 * time.Minute) }
	token, _ := tm.Issue("user-123")

	tm.now = time.Now
	if _, err := tm.Verify(token); err == nil {
		t.Fatal("expired token verified")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	tm := newTM(t)
	for _, s := range []string{"", "not.a.token", "a.b.c"} {
		if _, err := tm.Verify(s); err == nil {
			t.Errorf("garbage %q verified", s)
		}
	}
}

// A token signed with "alg":"none" must be rejected: Verify pins HS256.
func TestVerifyRejectsAlgNone(t *testing.T) {
	tm := newTM(t)
	claims := jwt.RegisteredClaims{
		Subject:   "attacker",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	token, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := tm.Verify(token); err == nil {
		t.Fatal("alg=none token verified")
	}
}

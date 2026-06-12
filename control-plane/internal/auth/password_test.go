package auth

import (
	"strings"
	"testing"
)

func newTestHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher([]byte("test-pepper"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}

func TestNewHasherRejectsEmptyPepper(t *testing.T) {
	if _, err := NewHasher(nil); err == nil {
		t.Fatal("expected error for empty pepper, got nil")
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	h := newTestHasher(t)
	encoded, err := h.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC prefix: %q", encoded)
	}

	ok, err := h.Verify("hunter2", encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("correct password did not verify")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	h := newTestHasher(t)
	encoded, err := h.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := h.Verify("wrong", encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("wrong password verified")
	}
}

func TestHashIsSaltedAndUnique(t *testing.T) {
	h := newTestHasher(t)
	a, _ := h.Hash("samepass")
	b, _ := h.Hash("samepass")
	if a == b {
		t.Fatal("identical passwords produced identical hashes; salt not applied")
	}
}

// The pepper is part of the secret: a hash made with one pepper must not verify
// under a different pepper, even with the correct password.
func TestPepperIsRequiredToVerify(t *testing.T) {
	h1, _ := NewHasher([]byte("pepper-one"))
	h2, _ := NewHasher([]byte("pepper-two"))

	encoded, err := h1.Hash("hunter2")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := h2.Verify("hunter2", encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("hash verified under the wrong pepper")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	h := newTestHasher(t)
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=3,p=4$only-salt", // missing hash segment
		"$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=999$m=65536,t=3,p=4$c2FsdA$aGFzaA", // bad version
		"$argon2id$v=19$bad-params$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!notb64$aGFzaA",
	}
	for _, c := range cases {
		if _, err := h.Verify("hunter2", c); err == nil {
			t.Errorf("expected error for malformed hash %q, got nil", c)
		}
	}
}

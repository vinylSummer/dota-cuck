package auth

import (
	"bytes"
	"testing"
)

func mustSalt(t *testing.T) []byte {
	t.Helper()
	salt, err := NewSalt(KDFSaltLen)
	if err != nil {
		t.Fatalf("NewSalt: %v", err)
	}
	return salt
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("login-password", mustSalt(t))
	plaintext := []byte("steam-secret-password")

	ct, nonce, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	got, err := Decrypt(key, ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestDeriveKeyIsDeterministicAndSaltDependent(t *testing.T) {
	salt := mustSalt(t)
	k1 := DeriveKey("pw", salt)
	k2 := DeriveKey("pw", salt)
	if !bytes.Equal(k1, k2) {
		t.Fatal("same password+salt produced different keys")
	}
	if len(k1) != 32 {
		t.Fatalf("key length = %d, want 32", len(k1))
	}

	k3 := DeriveKey("pw", mustSalt(t))
	if bytes.Equal(k1, k3) {
		t.Fatal("different salt produced same key")
	}

	k4 := DeriveKey("other-pw", salt)
	if bytes.Equal(k1, k4) {
		t.Fatal("different password produced same key")
	}
}

// The whole point of the model: a different login password cannot decrypt,
// mirroring a DB dump without the password.
func TestDecryptWithWrongKeyFails(t *testing.T) {
	salt := mustSalt(t)
	good := DeriveKey("correct-password", salt)
	plaintext := []byte("steam-secret")

	ct, nonce, err := Encrypt(good, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	wrong := DeriveKey("wrong-password", salt)
	if _, err := Decrypt(wrong, ct, nonce); err == nil {
		t.Fatal("decrypt succeeded with wrong key")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key := DeriveKey("pw", mustSalt(t))
	ct, nonce, err := Encrypt(key, []byte("steam-secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct[0] ^= 0xff // flip a bit; GCM auth tag must reject it
	if _, err := Decrypt(key, ct, nonce); err == nil {
		t.Fatal("decrypt accepted tampered ciphertext")
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key := DeriveKey("pw", mustSalt(t))
	_, n1, _ := Encrypt(key, []byte("x"))
	_, n2, _ := Encrypt(key, []byte("x"))
	if bytes.Equal(n1, n2) {
		t.Fatal("nonce reused across encryptions")
	}
}

func TestRejectsWrongKeySize(t *testing.T) {
	if _, _, err := Encrypt([]byte("short"), []byte("x")); err == nil {
		t.Fatal("Encrypt accepted a non-32-byte key")
	}
	if _, err := Decrypt([]byte("short"), []byte("x"), []byte("nonce")); err == nil {
		t.Fatal("Decrypt accepted a non-32-byte key")
	}
}

package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Steam passwords are encrypted at rest with AES-256-GCM. The 32-byte key is
// derived from the user's *login* password with Argon2id and a per-user salt,
// and is never stored — see the credential-security model in CLAUDE.md:
//
//	login_password ──Argon2id(salt)──▶ key ──AES-256-GCM──▶ enc_password+nonce
//
// The key lives only in memory for the duration of a request. A database dump
// holds the ciphertext, nonce, and KDF salt, but not the password, so it
// cannot recover the key. The KDF salt is not secret; it only stops the same
// password across users from producing the same key, and blocks precomputation.

// kdfParams mirror the password-hashing cost but exist independently so the two
// uses can be tuned separately. Output is 32 bytes for AES-256.
var kdfParams = PasswordParams{
	Time:    3,
	Memory:  64 * 1024,
	Threads: 4,
	KeyLen:  32,
	SaltLen: 16,
}

// KDFSaltLen is the length of the per-user key-derivation salt callers should
// generate (see NewSalt) and persist alongside the user.
const KDFSaltLen = 16

// NewSalt returns a cryptographically random salt of n bytes.
func NewSalt(n int) ([]byte, error) {
	salt := make([]byte, n)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("auth: read salt: %w", err)
	}
	return salt, nil
}

// DeriveKey derives the 32-byte AES-256 key from the login password and the
// user's KDF salt. The same (password, salt) pair always yields the same key,
// which is what lets the credential be decrypted on a later login.
func DeriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, kdfParams.Time, kdfParams.Memory, kdfParams.Threads, kdfParams.KeyLen)
}

// Encrypt seals plaintext with AES-256-GCM under key, returning the ciphertext
// (with the GCM auth tag appended) and the fresh random nonce. key must be 32
// bytes — use DeriveKey.
func Encrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("auth: read nonce: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt opens ciphertext produced by Encrypt. It returns an error if the key
// or nonce is wrong or the ciphertext was tampered with (GCM authentication).
func Decrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("auth: wrong nonce size")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: decrypt: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("auth: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("auth: new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

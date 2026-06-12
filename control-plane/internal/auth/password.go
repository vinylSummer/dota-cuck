package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password hashing uses Argon2id with two defences layered on the raw password:
//
//   - salt   — random per user, generated at hash time, stored in the encoded
//     string. Defeats precomputation (rainbow tables) and makes identical
//     passwords hash differently.
//   - pepper — a single server-wide secret, supplied via config and never
//     stored in the database. Mixed into the password with HMAC-SHA256 before
//     Argon2id. A database dump alone therefore can't be brute-forced offline;
//     the attacker also needs the pepper from the server's environment.
//
// The encoded output is the standard PHC string:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<b64-salt>$<b64-hash>
//
// so the parameters and salt travel with the hash and Verify needs only the
// password and pepper.

// PasswordParams are the Argon2id cost parameters. Defaults match the
// credential-security section of CLAUDE.md (time=3, memory=64MB, threads=4).
type PasswordParams struct {
	Time    uint32 // iterations
	Memory  uint32 // KiB
	Threads uint8
	KeyLen  uint32 // output length in bytes
	SaltLen uint32 // random salt length in bytes
}

// DefaultPasswordParams is used by NewHasher.
var DefaultPasswordParams = PasswordParams{
	Time:    3,
	Memory:  64 * 1024,
	Threads: 4,
	KeyLen:  32,
	SaltLen: 16,
}

// Hasher hashes and verifies user passwords. Construct it with NewHasher so the
// pepper is bound once; the same Hasher must be used for Hash and Verify.
type Hasher struct {
	pepper []byte
	params PasswordParams
}

// NewHasher binds the server pepper. The pepper must be non-empty: an empty
// pepper silently weakens the scheme to salt-only, so we reject it rather than
// let a misconfigured deployment look healthy.
func NewHasher(pepper []byte) (*Hasher, error) {
	if len(pepper) == 0 {
		return nil, errors.New("auth: password pepper must not be empty")
	}
	return &Hasher{pepper: pepper, params: DefaultPasswordParams}, nil
}

// pepperedKey mixes the pepper into the password with HMAC-SHA256. Using HMAC
// (rather than concatenation) avoids any ambiguity between password and pepper
// boundaries and yields a fixed-length input for Argon2id.
func (h *Hasher) pepperedKey(password string) []byte {
	mac := hmac.New(sha256.New, h.pepper)
	mac.Write([]byte(password))
	return mac.Sum(nil)
}

// Hash returns a PHC-encoded Argon2id hash of the password.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := h.derive(password, salt, h.params)
	return encodePHC(h.params, salt, key), nil
}

// Verify reports whether password matches the encoded PHC hash. The comparison
// is constant time. A malformed encoded string returns an error (distinct from
// a simple mismatch) so callers can tell corruption from a wrong password.
func (h *Hasher) Verify(password, encoded string) (bool, error) {
	params, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	got := h.derive(password, salt, params)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func (h *Hasher) derive(password string, salt []byte, p PasswordParams) []byte {
	return argon2.IDKey(h.pepperedKey(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

func encodePHC(p PasswordParams, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key),
	)
}

// decodePHC parses the PHC string Hash produced. It recovers the cost
// parameters and salt so Verify can recompute with the exact settings the hash
// was created under, which lets parameters change over time without breaking
// existing hashes.
func decodePHC(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// "" / argon2id / v=19 / m=..,t=..,p=.. / salt / hash
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, errors.New("auth: malformed password hash")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("auth: parse hash version: %w", err)
	}
	if version != argon2.Version {
		return PasswordParams{}, nil, nil, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	var p PasswordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("auth: parse hash params: %w", err)
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("auth: decode salt: %w", err)
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, fmt.Errorf("auth: decode hash: %w", err)
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	return p, salt, key, nil
}

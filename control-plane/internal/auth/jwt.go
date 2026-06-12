package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWT is the bearer token issued at login and checked on authenticated
// requests. Tokens are signed HS256 with a server secret; the only claim the
// API relies on is the subject (the user's UUID).

// signingMethod is fixed to HS256. Verify pins it explicitly so a forged token
// can't downgrade the algorithm (e.g. "alg":"none" or an RS256/HS256 confusion
// where the public key is used as the HMAC secret).
var signingMethod = jwt.SigningMethodHS256

// ErrInvalidToken is returned by Verify for any token that is malformed,
// expired, wrongly signed, or signed with an unexpected algorithm.
var ErrInvalidToken = errors.New("auth: invalid token")

// TokenManager issues and verifies JWTs. Construct it with NewTokenManager.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time // injectable for tests; defaults to time.Now
}

// NewTokenManager binds the signing secret and token lifetime. The secret must
// be non-empty.
func NewTokenManager(secret []byte, ttl time.Duration) (*TokenManager, error) {
	if len(secret) == 0 {
		return nil, errors.New("auth: jwt secret must not be empty")
	}
	if ttl <= 0 {
		return nil, errors.New("auth: jwt ttl must be positive")
	}
	return &TokenManager{secret: secret, ttl: ttl, now: time.Now}, nil
}

// Issue returns a signed token whose subject is userID, valid for the
// configured ttl from now.
func (tm *TokenManager) Issue(userID string) (string, error) {
	now := tm.now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(tm.ttl)),
	}
	token := jwt.NewWithClaims(signingMethod, claims)
	signed, err := token.SignedString(tm.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign token: %w", err)
	}
	return signed, nil
}

// Verify checks the token's signature, algorithm, and expiry, and returns the
// user ID from its subject. Any failure returns ErrInvalidToken.
func (tm *TokenManager) Verify(token string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return tm.secret, nil
	},
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
		jwt.WithTimeFunc(tm.now),
	)
	if err != nil || !parsed.Valid {
		return "", ErrInvalidToken
	}
	if claims.Subject == "" {
		return "", ErrInvalidToken
	}
	return claims.Subject, nil
}

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors the API layer maps to HTTP status codes. They keep pgx /
// Postgres specifics out of the handlers.
var (
	ErrUsernameTaken = errors.New("store: username already taken")
	ErrUserNotFound  = errors.New("store: user not found")
)

// uniqueViolation is the Postgres SQLSTATE for a unique-constraint breach.
const uniqueViolation = "23505"

// User is a persisted user row. PasswordHash is the PHC-encoded Argon2id hash;
// KDFSalt is the per-user salt for credential key derivation.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	KDFSalt      []byte
	CreatedAt    time.Time
}

// UserStore is the users-table data access.
type UserStore struct {
	pool *pgxpool.Pool
}

// Create inserts a new user and returns the database-generated id. It returns
// ErrUsernameTaken if the username is already in use.
func (s *UserStore) Create(ctx context.Context, username, passwordHash string, kdfSalt []byte) (string, error) {
	const q = `
		INSERT INTO users (username, password_hash, kdf_salt)
		VALUES ($1, $2, $3)
		RETURNING id`
	var id string
	err := s.pool.QueryRow(ctx, q, username, passwordHash, kdfSalt).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return "", ErrUsernameTaken
		}
		return "", fmt.Errorf("store: create user: %w", err)
	}
	return id, nil
}

// GetByUsername looks up a user by username. It returns ErrUserNotFound if no
// such user exists.
func (s *UserStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	const q = `
		SELECT id, username, password_hash, kdf_salt, created_at
		FROM users
		WHERE username = $1`
	var u User
	err := s.pool.QueryRow(ctx, q, username).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.KDFSalt, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("store: get user: %w", err)
	}
	return &u, nil
}

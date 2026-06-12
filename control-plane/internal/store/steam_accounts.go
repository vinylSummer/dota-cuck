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

var (
	ErrSteamAccountExists   = errors.New("store: user already has a linked steam account")
	ErrSteamAccountNotFound = errors.New("store: steam account not found")
)

// SteamAccount is a linked Steam account row. The password is stored encrypted
// (enc_password + enc_nonce); plaintext never touches the database.
type SteamAccount struct {
	ID            string
	UserID        string
	SteamID       string
	SteamUsername string
	EncPassword   []byte
	EncNonce      []byte
	SentryHash    []byte
	CreatedAt     time.Time
}

// SteamAccountStore is the steam_accounts-table data access.
type SteamAccountStore struct {
	pool *pgxpool.Pool
}

// Create links a Steam account to a user. V1 allows one per user; a second
// returns ErrSteamAccountExists.
func (s *SteamAccountStore) Create(ctx context.Context, userID, steamID, steamUsername string, encPassword, encNonce []byte) (string, error) {
	const q = `
		INSERT INTO steam_accounts (user_id, steam_id, steam_username, enc_password, enc_nonce)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`
	var id string
	err := s.pool.QueryRow(ctx, q, userID, steamID, steamUsername, encPassword, encNonce).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return "", ErrSteamAccountExists
		}
		return "", fmt.Errorf("store: create steam account: %w", err)
	}
	return id, nil
}

// GetByUserID returns the user's linked Steam account, or ErrSteamAccountNotFound.
func (s *SteamAccountStore) GetByUserID(ctx context.Context, userID string) (*SteamAccount, error) {
	const q = `
		SELECT id, user_id, steam_id, steam_username, enc_password, enc_nonce, sentry_hash, created_at
		FROM steam_accounts
		WHERE user_id = $1`
	var a SteamAccount
	err := s.pool.QueryRow(ctx, q, userID).Scan(
		&a.ID, &a.UserID, &a.SteamID, &a.SteamUsername,
		&a.EncPassword, &a.EncNonce, &a.SentryHash, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSteamAccountNotFound
		}
		return nil, fmt.Errorf("store: get steam account: %w", err)
	}
	return &a, nil
}

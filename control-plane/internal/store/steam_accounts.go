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
// (enc_password + enc_nonce); plaintext never touches the database. SteamID is
// empty until backfilled from the worker's first login.
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

// Create links a Steam account to a user, storing the encrypted credentials.
// steam_id is left null and backfilled later via SetSteamID. V1 allows one
// account per user; a second returns ErrSteamAccountExists.
func (s *SteamAccountStore) Create(ctx context.Context, userID, steamUsername string, encPassword, encNonce []byte) (string, error) {
	const q = `
		INSERT INTO steam_accounts (user_id, steam_username, enc_password, enc_nonce)
		VALUES ($1, $2, $3, $4)
		RETURNING id`
	var id string
	err := s.pool.QueryRow(ctx, q, userID, steamUsername, encPassword, encNonce).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return "", ErrSteamAccountExists
		}
		return "", fmt.Errorf("store: create steam account: %w", err)
	}
	return id, nil
}

// SetSteamID backfills the account's SteamID64 once the worker reports it.
func (s *SteamAccountStore) SetSteamID(ctx context.Context, id, steamID string) error {
	const q = `UPDATE steam_accounts SET steam_id = $2 WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, steamID)
	if err != nil {
		return fmt.Errorf("store: set steam_id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSteamAccountNotFound
	}
	return nil
}

// Delete removes the user's linked Steam account. Scoped by user_id so a user
// can only delete their own. Returns ErrSteamAccountNotFound if none matched.
func (s *SteamAccountStore) Delete(ctx context.Context, userID, id string) error {
	const q = `DELETE FROM steam_accounts WHERE id = $1 AND user_id = $2`
	tag, err := s.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return fmt.Errorf("store: delete steam account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSteamAccountNotFound
	}
	return nil
}

// GetByUserID returns the user's linked Steam account, or ErrSteamAccountNotFound.
func (s *SteamAccountStore) GetByUserID(ctx context.Context, userID string) (*SteamAccount, error) {
	const q = `
		SELECT id, user_id, steam_id, steam_username, enc_password, enc_nonce, sentry_hash, created_at
		FROM steam_accounts
		WHERE user_id = $1`
	var a SteamAccount
	var steamID *string // nullable until backfilled
	err := s.pool.QueryRow(ctx, q, userID).Scan(
		&a.ID, &a.UserID, &steamID, &a.SteamUsername,
		&a.EncPassword, &a.EncNonce, &a.SentryHash, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSteamAccountNotFound
		}
		return nil, fmt.Errorf("store: get steam account: %w", err)
	}
	if steamID != nil {
		a.SteamID = *steamID
	}
	return &a, nil
}

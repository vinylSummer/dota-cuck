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

// SteamAccount is a linked Steam account row. Auth uses the modern refresh-token
// model: the account's Steam refresh token is stored encrypted
// (enc_refresh_token + enc_refresh_nonce) — never the password, never a sentry.
// SteamID, SteamUsername, the token, and its expiry are all unknown at link
// creation time and backfilled once the worker completes the handshake.
type SteamAccount struct {
	ID                  string
	UserID              string
	SteamID             string
	SteamUsername       string
	EncRefreshToken     []byte
	EncRefreshNonce     []byte
	RefreshTokenExpires *time.Time
	CreatedAt           time.Time
}

// SteamAccountStore is the steam_accounts-table data access.
type SteamAccountStore struct {
	pool *pgxpool.Pool
}

// Create links a Steam account to a user. The row starts with no credentials;
// the refresh token, steam_id, and (for a QR link) the username are backfilled
// by SaveRefreshToken once the worker handshake completes. steamUsername may be
// empty (QR mode). V1 allows one account per user; a second returns
// ErrSteamAccountExists.
func (s *SteamAccountStore) Create(ctx context.Context, userID, steamUsername string) (string, error) {
	const q = `
		INSERT INTO steam_accounts (user_id, steam_username)
		VALUES ($1, $2)
		RETURNING id`
	var username *string
	if steamUsername != "" {
		username = &steamUsername
	}
	var id string
	err := s.pool.QueryRow(ctx, q, userID, username).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return "", ErrSteamAccountExists
		}
		return "", fmt.Errorf("store: create steam account: %w", err)
	}
	return id, nil
}

// SaveRefreshToken backfills the account with the outcome of a completed link:
// the encrypted refresh token (+ nonce), its expiry, the account's SteamID64,
// and — for a QR link where it was unknown up front — the resolved username.
// A nil expires or empty steamUsername leaves that column unchanged-as-null.
func (s *SteamAccountStore) SaveRefreshToken(ctx context.Context, id, steamID, steamUsername string, encToken, encNonce []byte, expires *time.Time) error {
	const q = `
		UPDATE steam_accounts
		SET enc_refresh_token = $2,
		    enc_refresh_nonce = $3,
		    refresh_token_expires = $4,
		    steam_id = $5,
		    steam_username = COALESCE(NULLIF($6, ''), steam_username)
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, encToken, encNonce, expires, steamID, steamUsername)
	if err != nil {
		return fmt.Errorf("store: save refresh token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSteamAccountNotFound
	}
	return nil
}

// SetSteamID backfills the account's SteamID64 once the worker reports it (e.g.
// from a friends login when it wasn't captured at link time).
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
		SELECT id, user_id, steam_id, steam_username,
		       enc_refresh_token, enc_refresh_nonce, refresh_token_expires, created_at
		FROM steam_accounts
		WHERE user_id = $1`
	var a SteamAccount
	var steamID, steamUsername *string // nullable until backfilled
	err := s.pool.QueryRow(ctx, q, userID).Scan(
		&a.ID, &a.UserID, &steamID, &steamUsername,
		&a.EncRefreshToken, &a.EncRefreshNonce, &a.RefreshTokenExpires, &a.CreatedAt,
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
	if steamUsername != nil {
		a.SteamUsername = *steamUsername
	}
	return &a, nil
}

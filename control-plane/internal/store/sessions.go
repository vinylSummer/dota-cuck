package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSessionNotFound is returned when a session row does not exist.
var ErrSessionNotFound = errors.New("store: session not found")

// Session is a spectator session row. match_id and webrtc_url are backfilled as
// the worker resolves the match and the stream comes up; started_at/ended_at
// bound the WATCHING window.
type Session struct {
	ID            string
	UserID        string
	TargetSteamID string
	MatchID       uint64
	State         string
	WebRTCURL     string
}

// SessionStore is the sessions-table data access.
type SessionStore struct {
	pool *pgxpool.Pool
}

// Create opens a session row for a user against a spectate target. It starts in
// the OFF state (the column default); the manager drives it through the
// lifecycle and writes each transition with SetState.
func (s *SessionStore) Create(ctx context.Context, userID, targetSteamID string) (string, error) {
	const q = `
		INSERT INTO sessions (user_id, target_steam_id)
		VALUES ($1, $2)
		RETURNING id`
	var id string
	if err := s.pool.QueryRow(ctx, q, userID, targetSteamID).Scan(&id); err != nil {
		return "", fmt.Errorf("store: create session: %w", err)
	}
	return id, nil
}

// SetState persists a session's lifecycle state.
func (s *SessionStore) SetState(ctx context.Context, id, state string) error {
	return s.exec(ctx, `UPDATE sessions SET state = $2 WHERE id = $1`, id, state)
}

// SetMatchID backfills the resolved live match id.
func (s *SessionStore) SetMatchID(ctx context.Context, id string, matchID uint64) error {
	return s.exec(ctx, `UPDATE sessions SET match_id = $2 WHERE id = $1`, id, int64(matchID))
}

// SetStream records the WebRTC URL and stamps started_at as the stream goes live.
func (s *SessionStore) SetStream(ctx context.Context, id, webrtcURL string) error {
	return s.exec(ctx, `UPDATE sessions SET webrtc_url = $2, started_at = now() WHERE id = $1`, id, webrtcURL)
}

// MarkEnded records the terminal state and stamps ended_at.
func (s *SessionStore) MarkEnded(ctx context.Context, id, state string) error {
	return s.exec(ctx, `UPDATE sessions SET state = $2, ended_at = now() WHERE id = $1`, id, state)
}

// Get returns a session by id, or ErrSessionNotFound.
func (s *SessionStore) Get(ctx context.Context, id string) (*Session, error) {
	const q = `
		SELECT id, user_id, target_steam_id, match_id, state, webrtc_url
		FROM sessions
		WHERE id = $1`
	var sess Session
	var matchID *int64
	var webrtcURL *string
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&sess.ID, &sess.UserID, &sess.TargetSteamID, &matchID, &sess.State, &webrtcURL,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("store: get session: %w", err)
	}
	if matchID != nil {
		sess.MatchID = uint64(*matchID)
	}
	if webrtcURL != nil {
		sess.WebRTCURL = *webrtcURL
	}
	return &sess, nil
}

func (s *SessionStore) exec(ctx context.Context, q, id string, args ...any) error {
	tag, err := s.pool.Exec(ctx, q, append([]any{id}, args...)...)
	if err != nil {
		return fmt.Errorf("store: update session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/sessions"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// SessionProvider is the session lifecycle the HTTP handlers drive. The concrete
// implementation is the sessions.Manager (state machine + worker commands + WS).
type SessionProvider interface {
	Start(ctx context.Context, userID, targetSteamID, refreshToken string) (*sessions.Info, error)
	Get(userID, id string) (*sessions.Info, bool)
	Stop(userID, id string) error
	SubmitGuard(userID, id, code string) error
}

// CreateSession godoc
// @Summary      Start spectating a friend
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      SessionRequest  true  "target friend"
// @Success      201   {object}  Session
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse  "worker busy / no steam account"
// @Failure      502   {object}  ErrorResponse  "worker unavailable"
// @Router       /sessions [post]
func (s *Server) CreateSession(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req SessionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TargetSteamID == "" {
		writeError(w, http.StatusBadRequest, "target_steam_id is required")
		return
	}

	// The spectate login reuses the linked account's refresh token (same path as
	// friends): load the account, ensure the link is complete, and decrypt the
	// token with the user's cached key.
	account, err := s.steamAccounts.GetByUserID(r.Context(), uid)
	if errors.Is(err, store.ErrSteamAccountNotFound) {
		writeError(w, http.StatusConflict, "no steam account linked")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load steam account")
		return
	}
	if len(account.EncRefreshToken) == 0 {
		writeError(w, http.StatusConflict, "steam account link not complete")
		return
	}
	key, ok := s.keys.Get(uid)
	if !ok {
		writeError(w, http.StatusUnauthorized, "session expired, please log in again")
		return
	}
	refreshToken, err := auth.Decrypt(key, account.EncRefreshToken, account.EncRefreshNonce)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not decrypt credentials")
		return
	}

	info, err := s.sessions.Start(r.Context(), uid, req.TargetSteamID, string(refreshToken))
	if errors.Is(err, sessions.ErrBusy) {
		writeError(w, http.StatusConflict, "a session is already active")
		return
	}
	if err != nil {
		// No worker connected, or the command could not be sent.
		writeError(w, http.StatusBadGateway, "could not start session")
		return
	}
	writeJSON(w, http.StatusCreated, toSessionDTO(info))
}

// GetSession godoc
// @Summary      Get session status (webrtc_url present once WATCHING)
// @Tags         sessions
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "session id"
// @Success      200  {object}  Session
// @Failure      404  {object}  ErrorResponse
// @Router       /sessions/{id} [get]
func (s *Server) GetSession(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	info, ok := s.sessions.Get(uid, chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, toSessionDTO(info))
}

// DeleteSession godoc
// @Summary      Stop spectating
// @Tags         sessions
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "session id"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Failure      502  {object}  ErrorResponse  "worker unavailable"
// @Router       /sessions/{id} [delete]
func (s *Server) DeleteSession(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	err := s.sessions.Stop(uid, chi.URLParam(r, "id"))
	if errors.Is(err, sessions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not stop session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SubmitSteamGuard godoc
// @Summary      Submit a Steam Guard code for a starting session
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string             true  "session id"
// @Param        body  body      SteamGuardRequest  true  "guard code"
// @Success      204
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      502   {object}  ErrorResponse  "worker unavailable"
// @Router       /sessions/{id}/steamguard [post]
func (s *Server) SubmitSteamGuard(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req SteamGuardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	err := s.sessions.SubmitGuard(uid, chi.URLParam(r, "id"), req.Code)
	if errors.Is(err, sessions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not submit steam guard code")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toSessionDTO(i *sessions.Info) Session {
	return Session{
		ID:            i.ID,
		State:         i.State,
		TargetSteamID: i.TargetSteamID,
		MatchID:       i.MatchID,
		WebRTCURL:     i.WebRTCURL,
	}
}

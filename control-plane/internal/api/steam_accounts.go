package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// Steam account management: link, list, remove. Auth uses the modern
// refresh-token model — account link acquires a Steam refresh token, which is
// encrypted at rest with the user's cached credential key. The Steam password
// (credentials fallback) is forwarded to the worker for the handshake but never
// persisted; it is never returned.

// AddSteamAccount godoc
// @Summary      Link a Steam account
// @Description  With no credentials, starts a QR link (the frontend renders the
// @Description  challenge URL pushed over the WebSocket). With steam_username +
// @Description  steam_password, starts the email-only / no-2FA credentials link.
// @Tags         steam
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      SteamAccountRequest  true  "optional steam credentials"
// @Success      201   {object}  SteamAccount
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse  "account already linked"
// @Router       /steam/accounts [post]
func (s *Server) AddSteamAccount(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req SteamAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// QR mode = no credentials; credentials mode = both present. A username with
	// no password (or vice versa) is a malformed request.
	if (req.SteamUsername == "") != (req.SteamPassword == "") {
		writeError(w, http.StatusBadRequest, "steam_username and steam_password must be provided together")
		return
	}

	// The cached key encrypts the refresh token at link completion. Captured now
	// (not looked up later) because the link can outlive the cache TTL.
	key, ok := s.keys.Get(uid)
	if !ok {
		// Valid token but no cached key (e.g. server restarted). Re-login repopulates it.
		writeError(w, http.StatusUnauthorized, "session expired, please log in again")
		return
	}

	id, err := s.steamAccounts.Create(r.Context(), uid, req.SteamUsername)
	if errors.Is(err, store.ErrSteamAccountExists) {
		writeError(w, http.StatusConflict, "a steam account is already linked")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not link steam account")
		return
	}

	// Kick off the worker handshake that acquires the refresh token and reports
	// the account's Steam ID. It runs asynchronously because it waits on the user
	// (scanning the QR or submitting an emailed code); progress — the QR
	// challenge, any guard prompt, and the terminal result — is pushed over WS.
	s.startAccountLink(id, req.SteamUsername, req.SteamPassword, key)

	writeJSON(w, http.StatusCreated, SteamAccount{
		ID:            id,
		SteamUsername: req.SteamUsername,
	})
}

// startAccountLink begins the worker handshake for a freshly linked account and
// wires its asynchronous outcome to the WebSocket hub and the refresh-token
// backfill. key encrypts the refresh token at completion.
func (s *Server) startAccountLink(accountID, username, password string, key []byte) {
	if s.links == nil {
		return
	}
	reqID := newRequestID()
	s.setLinkReq(accountID, reqID)
	s.links.StartLink(reqID, username, password, LinkCallbacks{
		OnQrChallenge: func(challengeURL string) {
			s.hub.Broadcast(context.Background(), AccountQrEvent(accountID, challengeURL))
		},
		OnGuard: func(guardType string) {
			s.hub.Broadcast(context.Background(), AccountGuardEvent(accountID, guardType))
		},
		OnLinked: func(ownerSteamID, refreshToken string) {
			s.clearLinkReq(accountID)
			if err := s.persistRefreshToken(accountID, ownerSteamID, refreshToken, key); err != nil {
				s.hub.log.Warn("persist refresh token failed", "account_id", accountID, "err", err)
				s.hub.Broadcast(context.Background(), AccountErrorEvent(accountID, "LINK_FAILED", "could not store refresh token"))
				return
			}
			s.hub.Broadcast(context.Background(), AccountLinkedEvent(accountID, ownerSteamID))
		},
		OnError: func(err error) {
			s.clearLinkReq(accountID)
			s.hub.Broadcast(context.Background(), AccountErrorEvent(accountID, "LINK_FAILED", err.Error()))
		},
	})
}

// persistRefreshToken encrypts the refresh token with the user-derived key and
// saves it alongside the account's Steam ID and the token's expiry (from its JWT
// `exp`). The plaintext token is held only transiently here.
func (s *Server) persistRefreshToken(accountID, ownerSteamID, refreshToken string, key []byte) error {
	encToken, encNonce, err := auth.Encrypt(key, []byte(refreshToken))
	if err != nil {
		return err
	}
	return s.steamAccounts.SaveRefreshToken(
		context.Background(), accountID, ownerSteamID, "", encToken, encNonce, expiryFromJWT(refreshToken),
	)
}

// expiryFromJWT reads the `exp` (Unix seconds) claim from a Steam token JWT and
// returns it as a time, or nil if the token is malformed or has no expiry. The
// signature is not verified — this is only a hint for proactive re-prompting.
func expiryFromJWT(token string) *time.Time {
	parts := splitJWT(token)
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return nil
	}
	t := time.Unix(claims.Exp, 0).UTC()
	return &t
}

// splitJWT splits a compact JWT into its dot-separated segments.
func splitJWT(token string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	return append(parts, token[start:])
}

// SubmitAccountSteamGuard godoc
// @Summary      Submit a Steam Guard code for a linking Steam account
// @Tags         steam
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string             true  "steam account id"
// @Param        body  body      SteamGuardRequest  true  "guard code"
// @Success      204
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse  "no Steam Guard prompt in progress"
// @Failure      502   {object}  ErrorResponse  "worker error"
// @Router       /steam/accounts/{id}/steamguard [post]
func (s *Server) SubmitAccountSteamGuard(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := chi.URLParam(r, "id")

	var req SteamGuardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	// Authorize: the account must exist and belong to this user.
	account, err := s.steamAccounts.GetByUserID(r.Context(), uid)
	if errors.Is(err, store.ErrSteamAccountNotFound) || (err == nil && account.ID != id) {
		writeError(w, http.StatusNotFound, "steam account not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load steam account")
		return
	}

	reqID, ok := s.getLinkReq(id)
	if !ok {
		writeError(w, http.StatusConflict, "no steam guard prompt in progress")
		return
	}
	if err := s.links.SubmitGuardCode(reqID, req.Code); err != nil {
		writeError(w, http.StatusBadGateway, "could not submit steam guard code")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSteamAccounts godoc
// @Summary      List linked Steam accounts
// @Tags         steam
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   SteamAccount
// @Failure      401  {object}  ErrorResponse
// @Router       /steam/accounts [get]
func (s *Server) ListSteamAccounts(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	account, err := s.steamAccounts.GetByUserID(r.Context(), uid)
	if errors.Is(err, store.ErrSteamAccountNotFound) {
		writeJSON(w, http.StatusOK, []SteamAccount{})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list steam accounts")
		return
	}

	writeJSON(w, http.StatusOK, []SteamAccount{toSteamAccountDTO(account)})
}

// DeleteSteamAccount godoc
// @Summary      Remove a linked Steam account
// @Tags         steam
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "steam account id"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /steam/accounts/{id} [delete]
func (s *Server) DeleteSteamAccount(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := chi.URLParam(r, "id")

	err := s.steamAccounts.Delete(r.Context(), uid, id)
	if errors.Is(err, store.ErrSteamAccountNotFound) {
		writeError(w, http.StatusNotFound, "steam account not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove steam account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toSteamAccountDTO(a *store.SteamAccount) SteamAccount {
	return SteamAccount{
		ID:            a.ID,
		SteamID:       a.SteamID,
		SteamUsername: a.SteamUsername,
		CreatedAt:     a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// Steam account management: link, list, remove. The Steam password is encrypted
// at link time with the user's cached credential key and never returned.

// AddSteamAccount godoc
// @Summary      Link a Steam account
// @Tags         steam
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      SteamAccountRequest  true  "steam credentials"
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
	if req.SteamUsername == "" || req.SteamPassword == "" {
		writeError(w, http.StatusBadRequest, "steam_username and steam_password are required")
		return
	}

	key, ok := s.keys.Get(uid)
	if !ok {
		// Valid token but no cached key (e.g. server restarted). Re-login repopulates it.
		writeError(w, http.StatusUnauthorized, "session expired, please log in again")
		return
	}
	encPassword, encNonce, err := auth.Encrypt(key, []byte(req.SteamPassword))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt credentials")
		return
	}

	id, err := s.steamAccounts.Create(r.Context(), uid, req.SteamUsername, encPassword, encNonce)
	if errors.Is(err, store.ErrSteamAccountExists) {
		writeError(w, http.StatusConflict, "a steam account is already linked")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not link steam account")
		return
	}

	// Kick off the worker login that establishes the Steam Guard sentry and
	// reports the account's Steam ID. It runs asynchronously because it may pause
	// on an interactive Steam Guard prompt; progress is pushed over the WebSocket.
	s.startAccountLink(id, req.SteamUsername, req.SteamPassword)

	writeJSON(w, http.StatusCreated, SteamAccount{
		ID:            id,
		SteamUsername: req.SteamUsername,
	})
}

// startAccountLink begins the worker login for a freshly linked account and
// wires its asynchronous outcome to the WebSocket hub and the steam_id backfill.
func (s *Server) startAccountLink(accountID, username, password string) {
	if s.links == nil {
		return
	}
	reqID := newRequestID()
	s.setLinkReq(accountID, reqID)
	s.links.StartLink(reqID, username, password, LinkCallbacks{
		OnGuard: func(guardType string) {
			s.hub.Broadcast(context.Background(), AccountGuardEvent(accountID, guardType))
		},
		OnLinked: func(ownerSteamID string) {
			s.clearLinkReq(accountID)
			if ownerSteamID != "" {
				if err := s.steamAccounts.SetSteamID(context.Background(), accountID, ownerSteamID); err != nil {
					s.hub.log.Warn("backfill steam_id failed", "account_id", accountID, "err", err)
				}
			}
			s.hub.Broadcast(context.Background(), AccountLinkedEvent(accountID, ownerSteamID))
		},
		OnError: func(err error) {
			s.clearLinkReq(accountID)
			s.hub.Broadcast(context.Background(), AccountErrorEvent(accountID, "LINK_FAILED", err.Error()))
		},
	})
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

package api

import (
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

	writeJSON(w, http.StatusCreated, SteamAccount{
		ID:            id,
		SteamUsername: req.SteamUsername,
	})
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

package api

import (
	"errors"
	"net/http"
	"sort"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// ListFriends godoc
// @Summary      List Steam friends with online and in-match status
// @Tags         friends
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   Friend
// @Failure      401  {object}  ErrorResponse
// @Failure      409  {object}  ErrorResponse  "no steam account linked"
// @Failure      502  {object}  ErrorResponse  "worker/steam error"
// @Router       /friends [get]
func (s *Server) ListFriends(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	account, err := s.steamAccounts.GetByUserID(r.Context(), uid)
	if errors.Is(err, store.ErrSteamAccountNotFound) {
		writeError(w, http.StatusConflict, "no steam account linked")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load steam account")
		return
	}

	key, ok := s.keys.Get(uid)
	if !ok {
		writeError(w, http.StatusUnauthorized, "session expired, please log in again")
		return
	}
	password, err := auth.Decrypt(key, account.EncPassword, account.EncNonce)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not decrypt credentials")
		return
	}

	list, err := s.friends.ListFriends(r.Context(), account.SteamUsername, string(password), account.SentryHash)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not fetch friends")
		return
	}

	// Backfill the account's own Steam ID the first time we learn it.
	if account.SteamID == "" && list.OwnerSteamID != "" {
		if err := s.steamAccounts.SetSteamID(r.Context(), account.ID, list.OwnerSteamID); err != nil {
			// Non-fatal: the friends list is still valid without the backfill.
			s.hub.log.Warn("backfill steam_id failed", "account_id", account.ID, "err", err)
		}
	}

	out := make([]Friend, len(list.Friends))
	for i, f := range list.Friends {
		out[i] = Friend{
			SteamID:     f.SteamID,
			PersonaName: f.PersonaName,
			Online:      f.Online,
			InMatch:     f.InMatch,
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PersonaName < out[j].PersonaName })
	writeJSON(w, http.StatusOK, out)
}

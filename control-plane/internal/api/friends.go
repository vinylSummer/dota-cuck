package api

import (
	"errors"
	"net/http"

	"github.com/vinylSummer/dota-cuck/internal/store"
)

// ListFriends godoc
// @Summary      List Steam friends with online and in-match status
// @Tags         friends
// @Produce      json
// @Success      200  {array}   Friend
// @Failure      401  {object}  ErrorResponse
// @Failure      409  {object}  ErrorResponse  "no steam account linked"
// @Failure      502  {object}  ErrorResponse  "steam api error"
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

	friends, err := s.steam.Friends(r.Context(), account.SteamID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "steam api error")
		return
	}

	out := make([]Friend, len(friends))
	for i, f := range friends {
		out[i] = Friend{
			SteamID:     f.SteamID,
			PersonaName: f.PersonaName,
			Online:      f.Online,
			InMatch:     f.InMatch,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

package api

import "net/http"

// HTTP handlers. Those for features not yet built return 501 (friends: step 6,
// steam/sessions: later steps). Auth (register/login) is implemented in
// auth.go. The swaggo annotations above each handler are the source for the
// generated OpenAPI spec.

// notImplemented writes a 501 with an ErrorResponse-shaped body.
func notImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, `{"error":"not implemented"}`, http.StatusNotImplemented)
}

// Logout godoc
// @Summary      Log out the current session
// @Tags         auth
// @Produce      json
// @Success      204
// @Router       /auth/logout [post]
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// ListSteamAccounts godoc
// @Summary      List linked Steam accounts
// @Tags         steam
// @Produce      json
// @Success      200  {array}   SteamAccount
// @Failure      401  {object}  ErrorResponse
// @Router       /steam/accounts [get]
func (s *Server) ListSteamAccounts(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// AddSteamAccount godoc
// @Summary      Link a Steam account
// @Tags         steam
// @Accept       json
// @Produce      json
// @Param        body  body      SteamAccountRequest  true  "steam credentials"
// @Success      201   {object}  SteamAccount
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /steam/accounts [post]
func (s *Server) AddSteamAccount(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// DeleteSteamAccount godoc
// @Summary      Remove a linked Steam account
// @Tags         steam
// @Produce      json
// @Param        id   path      string  true  "steam account id"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Router       /steam/accounts/{id} [delete]
func (s *Server) DeleteSteamAccount(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// ListFriends is implemented in friends.go.

// CreateSession godoc
// @Summary      Start spectating a friend
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Param        body  body      SessionRequest  true  "target friend"
// @Success      201   {object}  Session
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse  "worker busy"
// @Router       /sessions [post]
func (s *Server) CreateSession(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// GetSession godoc
// @Summary      Get session status (webrtc_url present once WATCHING)
// @Tags         sessions
// @Produce      json
// @Param        id   path      string  true  "session id"
// @Success      200  {object}  Session
// @Failure      404  {object}  ErrorResponse
// @Router       /sessions/{id} [get]
func (s *Server) GetSession(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// DeleteSession godoc
// @Summary      Stop spectating
// @Tags         sessions
// @Produce      json
// @Param        id   path      string  true  "session id"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Router       /sessions/{id} [delete]
func (s *Server) DeleteSession(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

// SubmitSteamGuard godoc
// @Summary      Submit a Steam Guard code for a starting session
// @Tags         sessions
// @Accept       json
// @Produce      json
// @Param        id    path      string             true  "session id"
// @Param        body  body      SteamGuardRequest  true  "guard code"
// @Success      204
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Router       /sessions/{id}/steamguard [post]
func (s *Server) SubmitSteamGuard(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }

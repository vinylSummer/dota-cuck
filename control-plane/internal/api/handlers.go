package api

import "net/http"

// HTTP handlers. Those for features not yet built return 501 (sessions: later
// steps). Auth (register/login/logout) is in auth.go; friends in friends.go;
// steam account management in steam_accounts.go. The swaggo annotations above
// each handler are the source for the generated OpenAPI spec.

// notImplemented writes a 501 with an ErrorResponse-shaped body.
func notImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, `{"error":"not implemented"}`, http.StatusNotImplemented)
}

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

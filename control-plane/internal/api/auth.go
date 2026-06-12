package api

import (
	"errors"
	"net/http"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// Auth handlers: register and login. Both end by issuing a JWT the client uses
// as a bearer token on authenticated routes.

// Register godoc
// @Summary      Register a new user
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      RegisterRequest  true  "credentials"
// @Success      201   {object}  LoginResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse  "username taken"
// @Router       /auth/register [post]
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	// Per-user salt for deriving the credential-encryption key from the login
	// password later (see internal/auth/crypto.go). Stored now; used when a
	// Steam account is linked.
	salt, err := auth.NewSalt(auth.KDFSaltLen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate salt")
		return
	}

	id, err := s.users.Create(r.Context(), req.Username, hash, salt)
	if errors.Is(err, store.ErrUsernameTaken) {
		writeError(w, http.StatusConflict, "username already taken")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	s.issueToken(w, id, http.StatusCreated)
}

// Login godoc
// @Summary      Log in and receive a JWT
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "credentials"
// @Success      200   {object}  LoginResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /auth/login [post]
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := s.users.GetByUsername(r.Context(), req.Username)
	if errors.Is(err, store.ErrUserNotFound) {
		// Same response as a wrong password: do not reveal which usernames exist.
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	ok, err := s.hasher.Verify(req.Password, user.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	s.issueToken(w, user.ID, http.StatusOK)
}

// issueToken signs a JWT for userID and writes it as a LoginResponse.
func (s *Server) issueToken(w http.ResponseWriter, userID string, status int) {
	token, err := s.tokens.Issue(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	writeJSON(w, status, LoginResponse{Token: token})
}

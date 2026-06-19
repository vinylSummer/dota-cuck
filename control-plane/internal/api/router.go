package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// UserStore is the slice of user persistence the auth handlers need.
type UserStore interface {
	Create(ctx context.Context, username, passwordHash string, kdfSalt []byte) (string, error)
	GetByUsername(ctx context.Context, username string) (*store.User, error)
}

// SteamAccountStore is the slice of steam-account persistence the handlers need.
type SteamAccountStore interface {
	Create(ctx context.Context, userID, steamUsername string) (string, error)
	GetByUserID(ctx context.Context, userID string) (*store.SteamAccount, error)
	SetSteamID(ctx context.Context, id, steamID string) error
	SaveRefreshToken(ctx context.Context, id, steamID, steamUsername string, encToken, encNonce []byte, expires *time.Time) error
	Delete(ctx context.Context, userID, id string) error
}

// FriendStatus is one friend with derived live status.
type FriendStatus struct {
	SteamID     string
	PersonaName string
	Online      bool
	InMatch     bool
}

// FriendList is the result of a friends fetch: the friends plus the logged-in
// account's own Steam ID (used to backfill steam_accounts.steam_id).
type FriendList struct {
	OwnerSteamID string
	Friends      []FriendStatus
}

// FriendsProvider fetches friends using a Steam refresh token (decrypted in
// memory by the control plane). The concrete implementation drives the worker
// over gRPC (an authenticated Steam session).
type FriendsProvider interface {
	ListFriends(ctx context.Context, refreshToken string) (*FriendList, error)
}

// LinkCallbacks receive the asynchronous outcome of a StartLink. OnQrChallenge
// (QR path) or OnGuard (credentials path) may fire before the terminal
// callback; then exactly one of OnLinked / OnError is called. They may run on a
// different goroutine than StartLink's caller.
type LinkCallbacks struct {
	OnQrChallenge func(challengeURL string)
	OnGuard       func(guardType string)
	OnLinked      func(ownerSteamID, refreshToken string)
	OnError       func(err error)
}

// LinkProvider drives a worker login to acquire a Steam refresh token and report
// the account's Steam ID. Empty username/password starts a QR link (OnQrChallenge
// receives the URL); credentials start the email/no-2FA path (OnGuard fires when
// a code is required). StartLink returns immediately; the result arrives via
// callbacks. SubmitGuardCode relays a code to the in-flight credentials login by
// request id. The concrete implementation drives the worker over gRPC.
type LinkProvider interface {
	StartLink(reqID, username, password string, cb LinkCallbacks)
	SubmitGuardCode(reqID, code string) error
}

// Server holds the HTTP handler dependencies. Handlers for features not yet
// built (steam linking, sessions) are still 501 stubs; the route surface is
// wired so the API shape is locked, documented, and testable.
type Server struct {
	hub           *Hub
	users         UserStore
	steamAccounts SteamAccountStore
	friends       FriendsProvider
	links         LinkProvider
	hasher        *auth.Hasher
	tokens        *auth.TokenManager
	keys          *auth.KeyCache

	// linkReqs maps an account id to the request id of its in-flight link, so a
	// Steam Guard submission for that account can be routed to the right login.
	linkMu   sync.Mutex
	linkReqs map[string]string
}

// Deps are the Server's collaborators. Grouped in a struct so the constructor
// signature stays stable as more are added.
type Deps struct {
	Hub           *Hub
	Users         UserStore
	SteamAccounts SteamAccountStore
	Friends       FriendsProvider
	Links         LinkProvider
	Hasher        *auth.Hasher
	Tokens        *auth.TokenManager
	Keys          *auth.KeyCache
}

func NewServer(d Deps) *Server {
	return &Server{
		hub:           d.Hub,
		users:         d.Users,
		steamAccounts: d.SteamAccounts,
		friends:       d.Friends,
		links:         d.Links,
		hasher:        d.Hasher,
		tokens:        d.Tokens,
		keys:          d.Keys,
		linkReqs:      make(map[string]string),
	}
}

func (s *Server) setLinkReq(accountID, reqID string) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	s.linkReqs[accountID] = reqID
}

func (s *Server) getLinkReq(accountID string) (string, bool) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	reqID, ok := s.linkReqs[accountID]
	return reqID, ok
}

func (s *Server) clearLinkReq(accountID string) {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	delete(s.linkReqs, accountID)
}

// newRequestID returns a random hex id used to correlate a link with its
// asynchronous worker reply and Steam Guard prompt.
func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Router builds the Chi mux with every route from the HTTP API spec, plus the
// Swagger UI at /docs.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		// Public: no token required.
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", s.Register)
			r.Post("/login", s.Login)
			// Logout is authenticated so it can evict the user's cached key.
			r.With(s.requireAuth).Post("/logout", s.Logout)
		})

		// Authenticated routes.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Route("/steam/accounts", func(r chi.Router) {
				r.Get("/", s.ListSteamAccounts)
				r.Post("/", s.AddSteamAccount)
				r.Delete("/{id}", s.DeleteSteamAccount)
				r.Post("/{id}/steamguard", s.SubmitAccountSteamGuard)
			})
			r.Get("/friends", s.ListFriends)
			r.Route("/sessions", func(r chi.Router) {
				r.Post("/", s.CreateSession)
				r.Get("/{id}", s.GetSession)
				r.Delete("/{id}", s.DeleteSession)
				r.Post("/{id}/steamguard", s.SubmitSteamGuard)
			})
		})
	})

	r.Get("/ws", s.hub.ServeHTTP)

	// Swagger UI + raw spec at /docs (spec at /docs/doc.json).
	r.Get("/docs", http.RedirectHandler("/docs/index.html", http.StatusMovedPermanently).ServeHTTP)
	r.Get("/docs/*", httpSwagger.Handler(httpSwagger.URL("/docs/doc.json")))

	return r
}

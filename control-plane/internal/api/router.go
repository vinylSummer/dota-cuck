package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/steam"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// UserStore is the slice of user persistence the auth handlers need.
type UserStore interface {
	Create(ctx context.Context, username, passwordHash string, kdfSalt []byte) (string, error)
	GetByUsername(ctx context.Context, username string) (*store.User, error)
}

// SteamAccountStore is the slice of steam-account persistence the handlers need.
type SteamAccountStore interface {
	GetByUserID(ctx context.Context, userID string) (*store.SteamAccount, error)
}

// FriendsProvider fetches a Steam account's friends with live status. The
// concrete implementation is *steam.Client.
type FriendsProvider interface {
	Friends(ctx context.Context, steamID string) ([]steam.Friend, error)
}

// Server holds the HTTP handler dependencies. Handlers for features not yet
// built (steam linking, sessions) are still 501 stubs; the route surface is
// wired so the API shape is locked, documented, and testable.
type Server struct {
	hub           *Hub
	users         UserStore
	steamAccounts SteamAccountStore
	steam         FriendsProvider
	hasher        *auth.Hasher
	tokens        *auth.TokenManager
	keys          *auth.KeyCache
}

// Deps are the Server's collaborators. Grouped in a struct so the constructor
// signature stays stable as more are added.
type Deps struct {
	Hub           *Hub
	Users         UserStore
	SteamAccounts SteamAccountStore
	Steam         FriendsProvider
	Hasher        *auth.Hasher
	Tokens        *auth.TokenManager
	Keys          *auth.KeyCache
}

func NewServer(d Deps) *Server {
	return &Server{
		hub:           d.Hub,
		users:         d.Users,
		steamAccounts: d.SteamAccounts,
		steam:         d.Steam,
		hasher:        d.Hasher,
		tokens:        d.Tokens,
		keys:          d.Keys,
	}
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

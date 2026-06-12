package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
)

// UserStore is the slice of persistence the auth handlers need. The concrete
// implementation is *store.UserStore; the interface keeps handlers testable
// with a fake and free of a database dependency.
type UserStore interface {
	Create(ctx context.Context, username, passwordHash string, kdfSalt []byte) (string, error)
	GetByUsername(ctx context.Context, username string) (*store.User, error)
}

// Server holds the HTTP handler dependencies. Handlers for features not yet
// built (steam, friends, sessions) are still 501 stubs; the route surface is
// wired so the API shape is locked, documented, and testable.
type Server struct {
	hub    *Hub
	users  UserStore
	hasher *auth.Hasher
	tokens *auth.TokenManager
}

// Deps are the Server's collaborators. Grouped in a struct so the constructor
// signature stays stable as more are added.
type Deps struct {
	Hub    *Hub
	Users  UserStore
	Hasher *auth.Hasher
	Tokens *auth.TokenManager
}

func NewServer(d Deps) *Server {
	return &Server{hub: d.Hub, users: d.Users, hasher: d.Hasher, tokens: d.Tokens}
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
			r.Post("/logout", s.Logout)
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

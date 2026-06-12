package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// Server holds the HTTP handler dependencies. Handlers are skeleton stubs
// (501) until their feature steps land; the route surface is wired now so the
// API shape is locked, documented, and testable.
type Server struct {
	hub *Hub
}

func NewServer(hub *Hub) *Server {
	return &Server{hub: hub}
}

// Router builds the Chi mux with every route from the HTTP API spec, plus the
// Swagger UI at /docs.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", s.Register)
			r.Post("/login", s.Login)
			r.Post("/logout", s.Logout)
		})
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

	r.Get("/ws", s.hub.ServeHTTP)

	// Swagger UI + raw spec at /docs (spec at /docs/doc.json).
	r.Get("/docs", http.RedirectHandler("/docs/index.html", http.StatusMovedPermanently).ServeHTTP)
	r.Get("/docs/*", httpSwagger.Handler(httpSwagger.URL("/docs/doc.json")))

	return r
}

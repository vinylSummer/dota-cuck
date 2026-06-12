package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server holds the HTTP handler dependencies. Handlers are skeleton stubs
// (501) until their feature steps land; the route surface is wired now so the
// API shape is locked and testable.
type Server struct {
	hub *Hub
}

func NewServer(hub *Hub) *Server {
	return &Server{hub: hub}
}

// Router builds the Chi mux with every route from the HTTP API spec.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", notImplemented)
			r.Post("/login", notImplemented)
			r.Post("/logout", notImplemented)
		})
		r.Route("/steam/accounts", func(r chi.Router) {
			r.Get("/", notImplemented)
			r.Post("/", notImplemented)
			r.Delete("/{id}", notImplemented)
		})
		r.Get("/friends", notImplemented)
		r.Route("/sessions", func(r chi.Router) {
			r.Post("/", notImplemented)
			r.Get("/{id}", notImplemented)
			r.Delete("/{id}", notImplemented)
			r.Post("/{id}/steamguard", notImplemented)
		})
	})

	r.Get("/ws", s.hub.ServeHTTP)
	return r
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

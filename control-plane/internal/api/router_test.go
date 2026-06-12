package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testRouter() http.Handler {
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewServer(hub).Router()
}

// Every documented route must be registered (501 = handler reached, not 404).
func TestDocumentedRoutesReturn501(t *testing.T) {
	routes := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/auth/register"},
		{http.MethodPost, "/api/auth/login"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodGet, "/api/steam/accounts"},
		{http.MethodPost, "/api/steam/accounts"},
		{http.MethodDelete, "/api/steam/accounts/abc"},
		{http.MethodGet, "/api/friends"},
		{http.MethodPost, "/api/sessions"},
		{http.MethodGet, "/api/sessions/abc"},
		{http.MethodDelete, "/api/sessions/abc"},
		{http.MethodPost, "/api/sessions/abc/steamguard"},
	}
	router := testRouter()
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status %d, want 501", rt.method, rt.path, rec.Code)
		}
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	router := testRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown route: status %d, want 404", rec.Code)
	}
}

func TestWrongMethodNotImplementedRouteIs405(t *testing.T) {
	// /api/friends is GET-only; a POST should be 405, proving the route exists
	// but the method does not.
	router := testRouter()
	req := httptest.NewRequest(http.MethodPost, "/api/friends", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/friends: status %d, want 405", rec.Code)
	}
}

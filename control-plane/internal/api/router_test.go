package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func testRouter(t *testing.T) http.Handler {
	srv, _ := newTestServer(t)
	return srv.Router()
}

// Authenticated routes reject a missing or invalid token with 401.
func TestProtectedRoutesRequireAuth(t *testing.T) {
	router := testRouter(t)
	cases := []struct {
		name, authHeader string
	}{
		{"no header", ""},
		{"garbage token", "Bearer not-a-jwt"},
		{"wrong scheme", "Basic abc"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/friends", nil)
		if c.authHeader != "" {
			req.Header.Set("Authorization", c.authHeader)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", c.name, rec.Code)
		}
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	router := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown route: status %d, want 404", rec.Code)
	}
}

func TestWrongMethodIs405(t *testing.T) {
	// /api/auth/register is POST-only; a GET should be 405, proving the route
	// exists but the method does not.
	router := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/register", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/auth/register: status %d, want 405", rec.Code)
	}
}

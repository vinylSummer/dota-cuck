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

// testRouterWithToken returns a router and a valid bearer token for it.
func testRouterWithToken(t *testing.T) (http.Handler, string) {
	srv, _ := newTestServer(t)
	token, err := srv.tokens.Issue("user-1")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return srv.Router(), token
}

// Routes for not-yet-built features must be registered (501 = handler reached,
// not 404). Auth routes are implemented and covered in auth_test.go.
func TestDocumentedRoutesReturn501(t *testing.T) {
	// Authenticated stubs (require a token to reach the handler).
	authed := []struct{ method, path string }{
		{http.MethodGet, "/api/steam/accounts"},
		{http.MethodPost, "/api/steam/accounts"},
		{http.MethodDelete, "/api/steam/accounts/abc"},
		{http.MethodPost, "/api/sessions"},
		{http.MethodGet, "/api/sessions/abc"},
		{http.MethodDelete, "/api/sessions/abc"},
		{http.MethodPost, "/api/sessions/abc/steamguard"},
	}

	router, token := testRouterWithToken(t)
	for _, rt := range authed {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status %d, want 501", rt.method, rt.path, rec.Code)
		}
	}
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

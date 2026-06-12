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

// Routes for not-yet-built features must be registered (501 = handler reached,
// not 404). Auth routes are implemented and covered in auth_test.go.
func TestDocumentedRoutesReturn501(t *testing.T) {
	routes := []struct {
		method, path string
	}{
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
	router := testRouter(t)
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
	router := testRouter(t)
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
	router := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/friends", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/friends: status %d, want 405", rec.Code)
	}
}

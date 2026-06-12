package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

// newTestServer builds a Server backed by a real throwaway Postgres database
// (via testdb) and real crypto. It returns the store too, so tests that need to
// simulate a database failure can close it.
func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st := testdb.New(t)
	hasher, err := auth.NewHasher([]byte("test-pepper"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	tokens, err := auth.NewTokenManager([]byte("test-secret"), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := NewServer(Deps{
		Hub:           hub,
		Users:         st.Users,
		SteamAccounts: st.SteamAccounts,
		Hasher:        hasher,
		Tokens:        tokens,
	})
	return srv, st
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRegisterSuccessReturnsToken(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register",
		RegisterRequest{Username: "alice", Password: "hunter2"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("empty token")
	}
}

func TestRegisterRejectsMissingFields(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, body := range []RegisterRequest{
		{Username: "", Password: "pw"},
		{Username: "alice", Password: ""},
	} {
		rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %+v: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestRegisterDuplicateReturns409(t *testing.T) {
	srv, _ := newTestServer(t)
	body := RegisterRequest{Username: "alice", Password: "hunter2"}
	doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register", body)
	rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestLoginSuccess(t *testing.T) {
	srv, _ := newTestServer(t)
	doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register",
		RegisterRequest{Username: "alice", Password: "hunter2"})

	rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/login",
		LoginRequest{Username: "alice", Password: "hunter2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("bad token response: %v / %s", err, rec.Body)
	}
}

func TestLoginWrongPasswordIs401(t *testing.T) {
	srv, _ := newTestServer(t)
	doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register",
		RegisterRequest{Username: "alice", Password: "hunter2"})

	rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/login",
		LoginRequest{Username: "alice", Password: "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// An unknown username must look identical to a wrong password (no user
// enumeration): same status, same body.
func TestLoginUnknownUserIs401SameAsWrongPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register",
		RegisterRequest{Username: "alice", Password: "hunter2"})

	wrongPass := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/login",
		LoginRequest{Username: "alice", Password: "wrong"})
	noUser := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/login",
		LoginRequest{Username: "ghost", Password: "whatever"})

	if wrongPass.Code != http.StatusUnauthorized || noUser.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d / %d, want 401 / 401", wrongPass.Code, noUser.Code)
	}
	if wrongPass.Body.String() != noUser.Body.String() {
		t.Fatalf("responses differ, enables user enumeration:\n wrong=%s\n nouser=%s",
			wrongPass.Body, noUser.Body)
	}
}

// A database failure surfaces as 500, not a leaked error or panic. Closing the
// pool makes the next query fail like a real outage.
func TestRegisterStoreErrorIs500(t *testing.T) {
	srv, st := newTestServer(t)
	st.Close()
	rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register",
		RegisterRequest{Username: "alice", Password: "hunter2"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

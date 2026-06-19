package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// registerAndToken registers a user and returns (userID, token). Registration
// caches the credential key, which AddSteamAccount needs.
func registerAndToken(t *testing.T, srv *Server) (string, string) {
	t.Helper()
	rec := doJSON(t, srv.Router(), http.MethodPost, "/api/auth/register",
		RegisterRequest{Username: "alice", Password: "hunter2"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d", rec.Code)
	}
	var resp LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	uid, err := srv.tokens.Verify(resp.Token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return uid, resp.Token
}

// doAuthedJSON sends a JSON request with a Bearer token.
func doAuthedJSON(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Linking creates the account row but persists no credentials: the Steam
// password is never stored, and the refresh token is backfilled only once the
// worker handshake completes (no worker is wired here, so it stays empty).
func TestAddSteamAccountCreatesRowWithoutCredentials(t *testing.T) {
	srv, st := newTestServer(t)
	uid, token := registerAndToken(t, srv)

	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token,
		SteamAccountRequest{SteamUsername: "alice_dota", SteamPassword: "s3cr3t"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var dto SteamAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.SteamUsername != "alice_dota" || dto.ID == "" {
		t.Fatalf("unexpected DTO: %+v", dto)
	}

	acct, err := st.SteamAccounts.GetByUserID(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if len(acct.EncRefreshToken) != 0 {
		t.Fatal("refresh token persisted before the link completed")
	}
}

// A QR link sends no credentials at all and is accepted.
func TestAddSteamAccountQrModeNoCredentials(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerAndToken(t, srv)
	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token,
		SteamAccountRequest{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
}

// A username without a password (or vice versa) is a malformed request.
func TestAddSteamAccountRejectsPartialCredentials(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerAndToken(t, srv)
	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token,
		SteamAccountRequest{SteamUsername: "alice_dota"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAddSteamAccountDuplicateIs409(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerAndToken(t, srv)
	body := SteamAccountRequest{SteamUsername: "alice_dota", SteamPassword: "s3cr3t"}
	doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token, body)
	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestListSteamAccounts(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerAndToken(t, srv)

	// Empty before linking.
	rec := doAuthedJSON(t, srv.Router(), http.MethodGet, "/api/steam/accounts", token, nil)
	var before []SteamAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("expected 0 accounts, got %d", len(before))
	}

	doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token,
		SteamAccountRequest{SteamUsername: "alice_dota", SteamPassword: "s3cr3t"})

	rec = doAuthedJSON(t, srv.Router(), http.MethodGet, "/api/steam/accounts", token, nil)
	var after []SteamAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(after) != 1 || after[0].SteamUsername != "alice_dota" {
		t.Fatalf("unexpected list: %+v", after)
	}
}

func TestDeleteSteamAccount(t *testing.T) {
	srv, _ := newTestServer(t)
	_, token := registerAndToken(t, srv)

	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token,
		SteamAccountRequest{SteamUsername: "alice_dota", SteamPassword: "s3cr3t"})
	var dto SteamAccount
	json.Unmarshal(rec.Body.Bytes(), &dto)

	del := doAuthedJSON(t, srv.Router(), http.MethodDelete, "/api/steam/accounts/"+dto.ID, token, nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.Code)
	}
	// Deleting a non-existent id is 404.
	again := doAuthedJSON(t, srv.Router(), http.MethodDelete, "/api/steam/accounts/"+dto.ID, token, nil)
	if again.Code != http.StatusNotFound {
		t.Fatalf("re-delete status = %d, want 404", again.Code)
	}
}

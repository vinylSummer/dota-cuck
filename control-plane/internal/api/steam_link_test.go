package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

// fakeLinks is a stand-in LinkProvider. It records what StartLink/SubmitGuardCode
// were called with and, via `fire`, lets a test drive the async callbacks
// synchronously (so there are no background goroutines to wait on).
type fakeLinks struct {
	mu        sync.Mutex
	reqID     string
	gotUser   string
	gotPass   string
	submitted []guardSubmit
	fire      func(reqID string, cb LinkCallbacks)
}

type guardSubmit struct{ reqID, code string }

func (f *fakeLinks) StartLink(reqID, username, password string, cb LinkCallbacks) {
	f.mu.Lock()
	f.reqID, f.gotUser, f.gotPass = reqID, username, password
	fire := f.fire
	f.mu.Unlock()
	if fire != nil {
		fire(reqID, cb)
	}
}

func (f *fakeLinks) SubmitGuardCode(reqID, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, guardSubmit{reqID, code})
	return nil
}

func newLinkServer(t *testing.T, fl LinkProvider) (*Server, *store.Store) {
	t.Helper()
	st := testdb.New(t)
	hasher, err := auth.NewHasher([]byte("pep"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	tokens, err := auth.NewTokenManager([]byte("sec"), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := NewServer(Deps{
		Hub:           hub,
		Users:         st.Users,
		SteamAccounts: st.SteamAccounts,
		Links:         fl,
		Hasher:        hasher,
		Tokens:        tokens,
		Keys:          auth.NewKeyCache(time.Hour),
	})
	return srv, st
}

// aRefreshToken is a syntactically valid (header.payload.signature) JWT whose
// payload has no exp — enough for the link path, which only stores it encrypted.
const aRefreshToken = "hdr.eyJzdWIiOiI3NjU2MTE5ODAwMDAwMDEyMyJ9.sig"

func addAccount(t *testing.T, srv *Server, token string) string {
	t.Helper()
	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts", token,
		SteamAccountRequest{SteamUsername: "alice_dota", SteamPassword: "s3cr3t"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add account status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var dto SteamAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return dto.ID
}

// Linking an account kicks off the worker login with the submitted credentials.
func TestAddSteamAccountStartsLink(t *testing.T) {
	fl := &fakeLinks{} // no callbacks fired: the link stays "in progress"
	srv, _ := newLinkServer(t, fl)
	_, token := registerAndToken(t, srv)

	addAccount(t, srv, token)

	if fl.gotUser != "alice_dota" || fl.gotPass != "s3cr3t" {
		t.Fatalf("StartLink got user=%q pass=%q, want alice_dota/s3cr3t", fl.gotUser, fl.gotPass)
	}
	if fl.reqID == "" {
		t.Fatal("expected a request id")
	}
}

// A successful link persists the encrypted refresh token, backfills steam_id,
// and clears the in-flight request.
func TestAddSteamAccountLinkSuccessPersistsRefreshToken(t *testing.T) {
	fl := &fakeLinks{fire: func(_ string, cb LinkCallbacks) {
		cb.OnLinked("76561198000000123", aRefreshToken)
	}}
	srv, st := newLinkServer(t, fl)
	uid, token := registerAndToken(t, srv)

	id := addAccount(t, srv, token)

	acct, err := st.SteamAccounts.GetByUserID(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if acct.SteamID != "76561198000000123" {
		t.Errorf("steam_id = %q, want backfilled", acct.SteamID)
	}
	// The refresh token is stored encrypted and decrypts back with the cached key.
	if len(acct.EncRefreshToken) == 0 {
		t.Fatal("refresh token not persisted")
	}
	key, ok := srv.keys.Get(uid)
	if !ok {
		t.Fatal("expected cached key")
	}
	plain, err := auth.Decrypt(key, acct.EncRefreshToken, acct.EncRefreshNonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(plain) != aRefreshToken {
		t.Fatalf("decrypted = %q, want the refresh token", plain)
	}
	// The in-flight request is cleared, so a guard submission now 409s.
	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts/"+id+"/steamguard", token,
		SteamGuardRequest{Code: "12345"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("guard after success = %d, want 409", rec.Code)
	}
}

// While a Steam Guard prompt is pending, the code is relayed to the worker.
func TestSubmitAccountSteamGuardRelaysCode(t *testing.T) {
	// Fire only OnGuard: the link stays pending, awaiting the code.
	fl := &fakeLinks{fire: func(_ string, cb LinkCallbacks) { cb.OnGuard("EMAIL") }}
	srv, _ := newLinkServer(t, fl)
	_, token := registerAndToken(t, srv)
	id := addAccount(t, srv, token)

	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts/"+id+"/steamguard", token,
		SteamGuardRequest{Code: "K4J9X"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if len(fl.submitted) != 1 || fl.submitted[0].code != "K4J9X" || fl.submitted[0].reqID != fl.reqID {
		t.Fatalf("unexpected submissions: %+v (reqID=%s)", fl.submitted, fl.reqID)
	}
}

// A guard submission with no link in progress is 409.
func TestSubmitAccountSteamGuardNoPendingIs409(t *testing.T) {
	fl := &fakeLinks{} // no OnGuard fired
	srv, _ := newLinkServer(t, fl)
	_, token := registerAndToken(t, srv)
	id := addAccount(t, srv, token)
	// Drain the pending request so there is nothing in progress.
	srv.clearLinkReq(id)

	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts/"+id+"/steamguard", token,
		SteamGuardRequest{Code: "12345"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// A guard submission for an account the caller does not own is 404.
func TestSubmitAccountSteamGuardWrongAccountIs404(t *testing.T) {
	fl := &fakeLinks{fire: func(_ string, cb LinkCallbacks) { cb.OnGuard("EMAIL") }}
	srv, _ := newLinkServer(t, fl)
	_, token := registerAndToken(t, srv)
	addAccount(t, srv, token)

	rec := doAuthedJSON(t, srv.Router(), http.MethodPost, "/api/steam/accounts/not-my-id/steamguard", token,
		SteamGuardRequest{Code: "12345"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// A failed link clears the in-flight request (no dangling guard slot).
func TestAddSteamAccountLinkErrorClearsRequest(t *testing.T) {
	fl := &fakeLinks{fire: func(_ string, cb LinkCallbacks) { cb.OnError(context.DeadlineExceeded) }}
	srv, _ := newLinkServer(t, fl)
	_, token := registerAndToken(t, srv)
	id := addAccount(t, srv, token)

	if _, ok := srv.getLinkReq(id); ok {
		t.Fatal("expected link request cleared after error")
	}
}

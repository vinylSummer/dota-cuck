package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/sessions"
	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

// fakeSessions is a stand-in SessionProvider (the real one is sessions.Manager).
type fakeSessions struct {
	startInfo *sessions.Info
	startErr  error
	gotToken  string
	gotTarget string

	getInfo *sessions.Info
	getOK   bool

	stopErr   error
	stopped   bool
	guardErr  error
	guardCode string
}

func (f *fakeSessions) Start(_ context.Context, _, target, token string) (*sessions.Info, error) {
	f.gotTarget, f.gotToken = target, token
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.startInfo, nil
}
func (f *fakeSessions) Get(_, _ string) (*sessions.Info, bool) { return f.getInfo, f.getOK }
func (f *fakeSessions) Stop(_, _ string) error                 { f.stopped = true; return f.stopErr }
func (f *fakeSessions) SubmitGuard(_, _, code string) error {
	f.guardCode = code
	return f.guardErr
}

type sessionsFixture struct {
	srv      *Server
	store    *store.Store
	sessions *fakeSessions
	keys     *auth.KeyCache
}

func newSessionsFixture(t *testing.T, fs *fakeSessions) *sessionsFixture {
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
	keys := auth.NewKeyCache(time.Hour)
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := NewServer(Deps{
		Hub:           hub,
		Users:         st.Users,
		SteamAccounts: st.SteamAccounts,
		Sessions:      fs,
		Hasher:        hasher,
		Tokens:        tokens,
		Keys:          keys,
	})
	return &sessionsFixture{srv: srv, store: st, sessions: fs, keys: keys}
}

func (f *sessionsFixture) seedUserWithSteam(t *testing.T, refreshToken string) string {
	t.Helper()
	ctx := context.Background()
	salt, _ := auth.NewSalt(auth.KDFSaltLen)
	uid, err := f.store.Users.Create(ctx, "alice", "hash", salt)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := auth.DeriveKey("login-pw", salt)
	f.keys.Put(uid, key)
	id, err := f.store.SteamAccounts.Create(ctx, uid, "alice_dota")
	if err != nil {
		t.Fatalf("create steam account: %v", err)
	}
	enc, nonce, err := auth.Encrypt(key, []byte(refreshToken))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := f.store.SteamAccounts.SaveRefreshToken(ctx, id, "76561198179568701", "", enc, nonce, nil); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	token, err := f.srv.tokens.Issue(uid)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func (f *sessionsFixture) seedUserNoSteam(t *testing.T) string {
	t.Helper()
	salt, _ := auth.NewSalt(auth.KDFSaltLen)
	uid, err := f.store.Users.Create(context.Background(), "bob", "hash", salt)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	f.keys.Put(uid, auth.DeriveKey("login-pw", salt))
	token, err := f.srv.tokens.Issue(uid)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func do(t *testing.T, srv *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestCreateSessionSuccess(t *testing.T) {
	fs := &fakeSessions{startInfo: &sessions.Info{
		ID: "sess-1", State: "STARTING", TargetSteamID: "76561198000000002",
	}}
	f := newSessionsFixture(t, fs)
	token := f.seedUserWithSteam(t, "refresh-tok-abc")

	rec := do(t, f.srv, http.MethodPost, "/api/sessions", token,
		SessionRequest{TargetSteamID: "76561198000000002"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var got Session
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "sess-1" || got.State != "STARTING" {
		t.Errorf("session = %+v", got)
	}
	// The handler decrypted the stored refresh token and passed it + the target through.
	if fs.gotToken != "refresh-tok-abc" {
		t.Errorf("provider got token=%q, want refresh-tok-abc", fs.gotToken)
	}
	if fs.gotTarget != "76561198000000002" {
		t.Errorf("provider got target=%q", fs.gotTarget)
	}
}

func TestCreateSessionMissingTarget(t *testing.T) {
	f := newSessionsFixture(t, &fakeSessions{})
	token := f.seedUserWithSteam(t, "tok")
	rec := do(t, f.srv, http.MethodPost, "/api/sessions", token, SessionRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateSessionNoSteamAccount(t *testing.T) {
	f := newSessionsFixture(t, &fakeSessions{})
	token := f.seedUserNoSteam(t)
	rec := do(t, f.srv, http.MethodPost, "/api/sessions", token,
		SessionRequest{TargetSteamID: "76561198000000002"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestCreateSessionBusyIs409(t *testing.T) {
	fs := &fakeSessions{startErr: sessions.ErrBusy}
	f := newSessionsFixture(t, fs)
	token := f.seedUserWithSteam(t, "tok")
	rec := do(t, f.srv, http.MethodPost, "/api/sessions", token,
		SessionRequest{TargetSteamID: "76561198000000002"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestCreateSessionWorkerUnavailableIs502(t *testing.T) {
	fs := &fakeSessions{startErr: errors.New("no worker connected")}
	f := newSessionsFixture(t, fs)
	token := f.seedUserWithSteam(t, "tok")
	rec := do(t, f.srv, http.MethodPost, "/api/sessions", token,
		SessionRequest{TargetSteamID: "76561198000000002"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestGetSessionFoundAndNotFound(t *testing.T) {
	fs := &fakeSessions{
		getInfo: &sessions.Info{ID: "sess-1", State: "WATCHING", WebRTCURL: "https://x/webrtc/live/match"},
		getOK:   true,
	}
	f := newSessionsFixture(t, fs)
	token := f.seedUserWithSteam(t, "tok")

	rec := do(t, f.srv, http.MethodGet, "/api/sessions/sess-1", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var got Session
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.WebRTCURL != "https://x/webrtc/live/match" {
		t.Errorf("webrtc = %q", got.WebRTCURL)
	}

	fs.getOK = false
	rec = do(t, f.srv, http.MethodGet, "/api/sessions/unknown", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeleteSession(t *testing.T) {
	fs := &fakeSessions{}
	f := newSessionsFixture(t, fs)
	token := f.seedUserWithSteam(t, "tok")

	rec := do(t, f.srv, http.MethodDelete, "/api/sessions/sess-1", token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !fs.stopped {
		t.Error("Stop not called")
	}

	fs.stopErr = sessions.ErrNotFound
	rec = do(t, f.srv, http.MethodDelete, "/api/sessions/sess-1", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSubmitSessionSteamGuard(t *testing.T) {
	fs := &fakeSessions{}
	f := newSessionsFixture(t, fs)
	token := f.seedUserWithSteam(t, "tok")

	rec := do(t, f.srv, http.MethodPost, "/api/sessions/sess-1/steamguard", token,
		SteamGuardRequest{Code: "K4J9X"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if fs.guardCode != "K4J9X" {
		t.Errorf("guard code = %q", fs.guardCode)
	}

	rec = do(t, f.srv, http.MethodPost, "/api/sessions/sess-1/steamguard", token,
		SteamGuardRequest{Code: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code status = %d, want 400", rec.Code)
	}
}

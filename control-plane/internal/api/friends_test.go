package api

import (
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
	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

// fakeFriends is a stand-in FriendsProvider (the real one drives the worker over
// gRPC). It records the refresh token it was called with so tests can assert the
// handler decrypted and passed it correctly.
type fakeFriends struct {
	result   *FriendList
	err      error
	gotToken string
}

func (f *fakeFriends) ListFriends(_ context.Context, refreshToken string) (*FriendList, error) {
	f.gotToken = refreshToken
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type friendsFixture struct {
	srv     *Server
	store   *store.Store
	friends *fakeFriends
	keys    *auth.KeyCache
}

func newFriendsFixture(t *testing.T, ff *fakeFriends) *friendsFixture {
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
		Friends:       ff,
		Hasher:        hasher,
		Tokens:        tokens,
		Keys:          keys,
	})
	return &friendsFixture{srv: srv, store: st, friends: ff, keys: keys}
}

// seedUserWithSteam creates a user, caches a credential key, links a Steam
// account with the given refresh token encrypted under that key, and returns
// (uid, token).
func (f *friendsFixture) seedUserWithSteam(t *testing.T, refreshToken string) (string, string) {
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
	// steam_id left empty so the success test can assert the handler backfills it.
	if err := f.store.SteamAccounts.SaveRefreshToken(ctx, id, "", "", enc, nonce, nil); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	token, err := f.srv.tokens.Issue(uid)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return uid, token
}

func (f *friendsFixture) seedUserNoSteam(t *testing.T) string {
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

func getFriends(t *testing.T, srv *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/friends", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestListFriendsSuccess(t *testing.T) {
	ff := &fakeFriends{result: &FriendList{
		OwnerSteamID: "76561198179568701",
		Friends: []FriendStatus{
			{SteamID: "11", PersonaName: "zoe", Online: true, InMatch: true},
			{SteamID: "22", PersonaName: "amy", Online: false, InMatch: false},
		},
	}}
	f := newFriendsFixture(t, ff)
	uid, token := f.seedUserWithSteam(t, "refresh-tok-xyz")

	rec := getFriends(t, f.srv, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var friends []Friend
	if err := json.Unmarshal(rec.Body.Bytes(), &friends); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Sorted by persona name: amy, zoe.
	if len(friends) != 2 || friends[0].PersonaName != "amy" || friends[1].PersonaName != "zoe" {
		t.Fatalf("unexpected friends: %+v", friends)
	}
	if !friends[1].Online || !friends[1].InMatch {
		t.Errorf("zoe should be online and in a match: %+v", friends[1])
	}

	// The handler must have decrypted the stored refresh token and passed it through.
	if ff.gotToken != "refresh-tok-xyz" {
		t.Fatalf("provider got token=%q, want refresh-tok-xyz", ff.gotToken)
	}

	// steam_id backfilled from owner_steam_id.
	acct, err := f.store.SteamAccounts.GetByUserID(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if acct.SteamID != "76561198179568701" {
		t.Errorf("steam_id = %q, want backfilled", acct.SteamID)
	}
}

func TestListFriendsNoLinkedAccountIs409(t *testing.T) {
	f := newFriendsFixture(t, &fakeFriends{})
	token := f.seedUserNoSteam(t)
	rec := getFriends(t, f.srv, token)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestListFriendsWorkerErrorIs502(t *testing.T) {
	f := newFriendsFixture(t, &fakeFriends{err: errors.New("no worker connected")})
	_, token := f.seedUserWithSteam(t, "refresh-tok-xyz")
	rec := getFriends(t, f.srv, token)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// A valid token but an empty key cache (e.g. after a server restart) is 401.
func TestListFriendsMissingKeyIs401(t *testing.T) {
	f := newFriendsFixture(t, &fakeFriends{result: &FriendList{}})
	uid, token := f.seedUserWithSteam(t, "refresh-tok-xyz")
	f.keys.Delete(uid)
	rec := getFriends(t, f.srv, token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

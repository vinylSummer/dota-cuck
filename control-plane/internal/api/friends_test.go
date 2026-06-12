package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/steam"
	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

// friendsFixture wires a Server backed by a real store and a real steam.Client
// pointed at a mock Steam Web API. steamHandler serves both Steam endpoints.
type friendsFixture struct {
	srv   *Server
	store *store.Store
	token string
}

func newFriendsFixture(t *testing.T, steamHandler http.Handler) *friendsFixture {
	t.Helper()
	st := testdb.New(t)

	mock := httptest.NewServer(steamHandler)
	t.Cleanup(mock.Close)
	steamClient := steam.NewClient("test-key", steam.WithBaseURL(mock.URL))

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
		Steam:         steamClient,
		Hasher:        hasher,
		Tokens:        tokens,
		Keys:          auth.NewKeyCache(time.Hour),
	})
	return &friendsFixture{srv: srv, store: st}
}

// seedUserWithSteam creates a user and a linked steam account, and returns a
// valid token for that user.
func (f *friendsFixture) seedUserWithSteam(t *testing.T, steamID string) string {
	t.Helper()
	ctx := context.Background()
	uid, err := f.store.Users.Create(ctx, "alice", "hash", []byte("salt-0123456789a"))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := f.store.SteamAccounts.Create(ctx, uid, steamID, "alice_dota", []byte("ct"), []byte("nonce")); err != nil {
		t.Fatalf("create steam account: %v", err)
	}
	token, err := f.srv.tokens.Issue(uid)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func (f *friendsFixture) seedUserNoSteam(t *testing.T) string {
	t.Helper()
	uid, err := f.store.Users.Create(context.Background(), "bob", "hash", []byte("salt-0123456789a"))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
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

// steamMux serves canned friend-list and summaries responses.
func steamMux(friendList string, summaries string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ISteamUser/GetFriendList/v1/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, friendList)
	})
	mux.HandleFunc("/ISteamUser/GetPlayerSummaries/v2/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, summaries)
	})
	return mux
}

func TestListFriendsSuccess(t *testing.T) {
	mux := steamMux(
		`{"friendslist":{"friends":[{"steamid":"11"},{"steamid":"22"}]}}`,
		`{"response":{"players":[
			{"steamid":"11","personaname":"zoe","personastate":1,"gameid":"570"},
			{"steamid":"22","personaname":"amy","personastate":0}
		]}}`)
	f := newFriendsFixture(t, mux)
	token := f.seedUserWithSteam(t, "76561198000000000")

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
	if friends[0].Online || friends[0].InMatch {
		t.Errorf("amy should be offline/not in match: %+v", friends[0])
	}
	if !friends[1].Online || !friends[1].InMatch {
		t.Errorf("zoe should be online and in a Dota match: %+v", friends[1])
	}
}

func TestListFriendsNoLinkedAccountIs409(t *testing.T) {
	f := newFriendsFixture(t, steamMux("", ""))
	token := f.seedUserNoSteam(t)

	rec := getFriends(t, f.srv, token)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestListFriendsSteamErrorIs502(t *testing.T) {
	// Steam returns 500 -> the client errors -> handler maps to 502.
	mux := http.NewServeMux()
	mux.HandleFunc("/ISteamUser/GetFriendList/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	f := newFriendsFixture(t, mux)
	token := f.seedUserWithSteam(t, "76561198000000000")

	rec := getFriends(t, f.srv, token)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

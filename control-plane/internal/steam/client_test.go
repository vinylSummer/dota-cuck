package steam

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockServer routes the two Steam endpoints to provided handlers and records
// the requests each received.
type mockServer struct {
	*httptest.Server
	friendListReqs []*http.Request
	summariesReqs  []*http.Request
}

func newMock(t *testing.T, friendListBody string, summariesBody func(steamids string) string) *mockServer {
	t.Helper()
	m := &mockServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ISteamUser/GetFriendList/v1/", func(w http.ResponseWriter, r *http.Request) {
		m.friendListReqs = append(m.friendListReqs, r)
		fmt.Fprint(w, friendListBody)
	})
	mux.HandleFunc("/ISteamUser/GetPlayerSummaries/v2/", func(w http.ResponseWriter, r *http.Request) {
		m.summariesReqs = append(m.summariesReqs, r)
		fmt.Fprint(w, summariesBody(r.URL.Query().Get("steamids")))
	})
	m.Server = httptest.NewServer(mux)
	t.Cleanup(m.Close)
	return m
}

func testClient(t *testing.T, m *mockServer) *Client {
	t.Helper()
	return NewClient("test-key", WithBaseURL(m.URL))
}

func TestGetFriendListParsesAndSendsParams(t *testing.T) {
	m := newMock(t,
		`{"friendslist":{"friends":[{"steamid":"1"},{"steamid":"2"}]}}`,
		func(string) string { return "" })
	c := testClient(t, m)

	ids, err := c.GetFriendList(context.Background(), "76561198000000000")
	if err != nil {
		t.Fatalf("GetFriendList: %v", err)
	}
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("ids = %v, want [1 2]", ids)
	}

	q := m.friendListReqs[0].URL.Query()
	if q.Get("key") != "test-key" {
		t.Errorf("key = %q", q.Get("key"))
	}
	if q.Get("steamid") != "76561198000000000" {
		t.Errorf("steamid = %q", q.Get("steamid"))
	}
	if q.Get("relationship") != "friend" {
		t.Errorf("relationship = %q", q.Get("relationship"))
	}
}

func TestFriendsDerivesStatusAndSorts(t *testing.T) {
	m := newMock(t,
		`{"friendslist":{"friends":[{"steamid":"1"},{"steamid":"2"},{"steamid":"3"}]}}`,
		func(string) string {
			return `{"response":{"players":[
				{"steamid":"1","personaname":"charlie","personastate":1,"gameid":"570"},
				{"steamid":"2","personaname":"alice","personastate":0},
				{"steamid":"3","personaname":"bob","personastate":3,"gameid":"730"}
			]}}`
		})
	c := testClient(t, m)

	friends, err := c.Friends(context.Background(), "me")
	if err != nil {
		t.Fatalf("Friends: %v", err)
	}
	// Sorted by persona name: alice, bob, charlie.
	if len(friends) != 3 || friends[0].PersonaName != "alice" ||
		friends[1].PersonaName != "bob" || friends[2].PersonaName != "charlie" {
		t.Fatalf("unexpected order: %+v", friends)
	}

	alice, bob, charlie := friends[0], friends[1], friends[2]
	if alice.Online || alice.InMatch {
		t.Errorf("alice: online=%v inMatch=%v, want false/false", alice.Online, alice.InMatch)
	}
	if !bob.Online || bob.InMatch {
		t.Errorf("bob: online=%v inMatch=%v, want true/false (in non-Dota game)", bob.Online, bob.InMatch)
	}
	if !charlie.Online || !charlie.InMatch {
		t.Errorf("charlie: online=%v inMatch=%v, want true/true (Dota app 570)", charlie.Online, charlie.InMatch)
	}
}

func TestFriendsEmptyListSkipsSummaries(t *testing.T) {
	m := newMock(t,
		`{"friendslist":{"friends":[]}}`,
		func(string) string { return `{"response":{"players":[]}}` })
	c := testClient(t, m)

	friends, err := c.Friends(context.Background(), "me")
	if err != nil {
		t.Fatalf("Friends: %v", err)
	}
	if len(friends) != 0 {
		t.Fatalf("friends = %v, want empty", friends)
	}
	if len(m.summariesReqs) != 0 {
		t.Fatalf("summaries called %d times for an empty friend list", len(m.summariesReqs))
	}
}

func TestGetPlayerSummariesBatchesOver100(t *testing.T) {
	// 250 ids => 3 batches (100, 100, 50). Each batch echoes its own steamids
	// back as players so we can verify counts and batch sizes.
	m := newMock(t, "", func(steamids string) string {
		var players []string
		for _, id := range strings.Split(steamids, ",") {
			players = append(players, fmt.Sprintf(`{"steamid":%q,"personaname":"p%s","personastate":1}`, id, id))
		}
		return `{"response":{"players":[` + strings.Join(players, ",") + `]}}`
	})
	c := testClient(t, m)

	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("%d", i)
	}
	summaries, err := c.GetPlayerSummaries(context.Background(), ids)
	if err != nil {
		t.Fatalf("GetPlayerSummaries: %v", err)
	}
	if len(summaries) != 250 {
		t.Fatalf("summaries = %d, want 250", len(summaries))
	}
	if len(m.summariesReqs) != 3 {
		t.Fatalf("batches = %d, want 3", len(m.summariesReqs))
	}
	sizes := []int{
		len(strings.Split(m.summariesReqs[0].URL.Query().Get("steamids"), ",")),
		len(strings.Split(m.summariesReqs[1].URL.Query().Get("steamids"), ",")),
		len(strings.Split(m.summariesReqs[2].URL.Query().Get("steamids"), ",")),
	}
	if sizes[0] != 100 || sizes[1] != 100 || sizes[2] != 50 {
		t.Fatalf("batch sizes = %v, want [100 100 50]", sizes)
	}
}

func TestGetFriendListNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // e.g. private friend list / bad key
	}))
	t.Cleanup(srv.Close)
	c := NewClient("k", WithBaseURL(srv.URL))

	if _, err := c.GetFriendList(context.Background(), "me"); err == nil {
		t.Fatal("expected error on non-200, got nil")
	}
}

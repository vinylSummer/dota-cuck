package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

// makeUser inserts a user and returns its id (steam_accounts.user_id FK).
func makeUser(t *testing.T, st *store.Store, name string) string {
	t.Helper()
	id, err := st.Users.Create(context.Background(), name, "hash", []byte("salt-0123456789a"))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

func TestSteamAccountCreateAndGet(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	uid := makeUser(t, st, "alice")

	id, err := st.SteamAccounts.Create(ctx, uid, "76561198000000000", "alice_dota", []byte("ct"), []byte("nonce"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := st.SteamAccounts.GetByUserID(ctx, uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.ID != id || got.SteamID != "76561198000000000" || got.SteamUsername != "alice_dota" {
		t.Fatalf("unexpected account: %+v", got)
	}
}

func TestSteamAccountOnePerUser(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	uid := makeUser(t, st, "alice")

	if _, err := st.SteamAccounts.Create(ctx, uid, "1", "a", []byte("c"), []byte("n")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := st.SteamAccounts.Create(ctx, uid, "2", "b", []byte("c"), []byte("n"))
	if !errors.Is(err, store.ErrSteamAccountExists) {
		t.Fatalf("second Create error = %v, want ErrSteamAccountExists", err)
	}
}

func TestSteamAccountNotFound(t *testing.T) {
	st := testdb.New(t)
	uid := makeUser(t, st, "alice") // user exists, but no steam account
	_, err := st.SteamAccounts.GetByUserID(context.Background(), uid)
	if !errors.Is(err, store.ErrSteamAccountNotFound) {
		t.Fatalf("error = %v, want ErrSteamAccountNotFound", err)
	}
}

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

	id, err := st.SteamAccounts.Create(ctx, uid, "alice_dota")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := st.SteamAccounts.GetByUserID(ctx, uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.ID != id || got.SteamUsername != "alice_dota" {
		t.Fatalf("unexpected account: %+v", got)
	}
	if got.SteamID != "" {
		t.Errorf("steam_id = %q, want empty until backfilled", got.SteamID)
	}
}

func TestSteamAccountSetSteamID(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	uid := makeUser(t, st, "alice")
	id, err := st.SteamAccounts.Create(ctx, uid, "alice_dota")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := st.SteamAccounts.SetSteamID(ctx, id, "76561198179568701"); err != nil {
		t.Fatalf("SetSteamID: %v", err)
	}
	got, err := st.SteamAccounts.GetByUserID(ctx, uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if got.SteamID != "76561198179568701" {
		t.Fatalf("steam_id = %q, want backfilled value", got.SteamID)
	}
}

func TestSteamAccountSaveRefreshToken(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	uid := makeUser(t, st, "alice")
	// QR link: created with no username, backfilled on completion.
	id, err := st.SteamAccounts.Create(ctx, uid, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	expires := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	if err := st.SteamAccounts.SaveRefreshToken(ctx, id, "76561198179568701", "alice_dota",
		[]byte("ciphertext"), []byte("nonce"), &expires); err != nil {
		t.Fatalf("SaveRefreshToken: %v", err)
	}

	got, err := st.SteamAccounts.GetByUserID(ctx, uid)
	if err != nil {
		t.Fatalf("GetByUserID: %v", err)
	}
	if string(got.EncRefreshToken) != "ciphertext" || string(got.EncRefreshNonce) != "nonce" {
		t.Fatalf("token round-trip mismatch: %+v", got)
	}
	if got.SteamID != "76561198179568701" || got.SteamUsername != "alice_dota" {
		t.Fatalf("backfill mismatch: steam_id=%q username=%q", got.SteamID, got.SteamUsername)
	}
	if got.RefreshTokenExpires == nil || !got.RefreshTokenExpires.Equal(expires) {
		t.Fatalf("expires = %v, want %v", got.RefreshTokenExpires, expires)
	}
}

func TestSteamAccountDelete(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	uid := makeUser(t, st, "alice")
	id, err := st.SteamAccounts.Create(ctx, uid, "alice_dota")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := st.SteamAccounts.Delete(ctx, uid, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.SteamAccounts.GetByUserID(ctx, uid); !errors.Is(err, store.ErrSteamAccountNotFound) {
		t.Fatalf("after delete, GetByUserID error = %v, want ErrSteamAccountNotFound", err)
	}
	// Deleting again (or another user's id) reports not found.
	if err := st.SteamAccounts.Delete(ctx, uid, id); !errors.Is(err, store.ErrSteamAccountNotFound) {
		t.Fatalf("re-delete error = %v, want ErrSteamAccountNotFound", err)
	}
}

func TestSteamAccountOnePerUser(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()
	uid := makeUser(t, st, "alice")

	if _, err := st.SteamAccounts.Create(ctx, uid, "a"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := st.SteamAccounts.Create(ctx, uid, "b")
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

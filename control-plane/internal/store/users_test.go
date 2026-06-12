package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/testdb"
)

func TestUserCreateAndGet(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()

	salt := []byte("0123456789abcdef")
	id, err := st.Users.Create(ctx, "alice", "phc-hash", salt)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create returned empty id")
	}

	got, err := st.Users.GetByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.ID != id {
		t.Errorf("id = %q, want %q", got.ID, id)
	}
	if got.Username != "alice" {
		t.Errorf("username = %q, want alice", got.Username)
	}
	if got.PasswordHash != "phc-hash" {
		t.Errorf("password_hash = %q, want phc-hash", got.PasswordHash)
	}
	if !bytes.Equal(got.KDFSalt, salt) {
		t.Errorf("kdf_salt = %x, want %x", got.KDFSalt, salt)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at not populated by the database default")
	}
}

func TestUserCreateDuplicateUsername(t *testing.T) {
	st := testdb.New(t)
	ctx := context.Background()

	if _, err := st.Users.Create(ctx, "alice", "h1", []byte("salt-aaaaaaaaaaa")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := st.Users.Create(ctx, "alice", "h2", []byte("salt-bbbbbbbbbbb"))
	if !errors.Is(err, store.ErrUsernameTaken) {
		t.Fatalf("duplicate Create error = %v, want ErrUsernameTaken", err)
	}
}

func TestUserGetByUsernameNotFound(t *testing.T) {
	st := testdb.New(t)
	_, err := st.Users.GetByUsername(context.Background(), "ghost")
	if !errors.Is(err, store.ErrUserNotFound) {
		t.Fatalf("error = %v, want ErrUserNotFound", err)
	}
}

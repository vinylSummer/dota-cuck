package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// freshDB creates an empty (unmigrated) throwaway database and returns its URL
// plus a cleanup. Unlike testdb.New it does NOT apply migrations — that is what
// these tests exercise.
func freshDB(t *testing.T) string {
	t.Helper()
	adminURL := os.Getenv("POSTGRESQL_URL")
	if adminURL == "" {
		t.Skip("POSTGRESQL_URL not set; run via make test-go")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)

	name := fmt.Sprintf("migrate_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		a, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			return
		}
		defer a.Close(ctx)
		_, _ = a.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0) // .../internal/store/migrate_test.go
	return filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations")
}

func TestMigrateAppliesSchemaThenIsIdempotent(t *testing.T) {
	dbURL := freshDB(t)
	dir := migrationsDir(t)
	ctx := context.Background()

	applied, err := Migrate(ctx, dbURL, dir)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("first Migrate applied nothing")
	}

	// The schema is usable: opening the store and querying a migrated table works.
	st, err := New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if _, err := st.SteamAccounts.GetByUserID(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		// ErrSteamAccountNotFound is the expected "table exists, no row" outcome.
		if err.Error() == "" {
			t.Fatalf("unexpected: %v", err)
		}
	}

	// Re-running applies nothing (idempotent).
	again, err := Migrate(ctx, dbURL, dir)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Migrate applied %v, want none", again)
	}
}

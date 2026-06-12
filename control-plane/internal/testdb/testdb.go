// Package testdb provides a real PostgreSQL-backed store for tests. Each call
// to New creates a fresh throwaway database on the instance at POSTGRESQL_URL,
// applies every migration, and drops the database on cleanup — so tests run
// against the same schema and engine as production, not a mock.
//
// Tests that use it require POSTGRESQL_URL to be set. Run them via `make test`,
// which spins up an ephemeral cluster (scripts/with-test-db.sh).
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vinylSummer/dota-cuck/internal/store"
)

// New returns a *store.Store bound to a fresh, migrated, throwaway database.
// The database is dropped when the test finishes.
func New(t *testing.T) *store.Store {
	t.Helper()
	adminURL := os.Getenv("POSTGRESQL_URL")
	if adminURL == "" {
		t.Fatal("POSTGRESQL_URL is not set; run DB tests via `make test` (spins up a test cluster)")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testdb: connect admin: %v", err)
	}
	defer admin.Close(ctx)

	dbName := fmt.Sprintf("dota_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}

	testURL := withDatabase(t, adminURL, dbName)
	applyMigrations(t, ctx, testURL)

	st, err := store.New(ctx, testURL)
	if err != nil {
		t.Fatalf("testdb: open store: %v", err)
	}

	t.Cleanup(func() {
		st.Close()
		a, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Logf("testdb: cleanup connect: %v", err)
			return
		}
		defer a.Close(ctx)
		// WITH (FORCE) terminates any lingering connections to the database.
		if _, err := a.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("testdb: drop database %s: %v", dbName, err)
		}
	})
	return st
}

// withDatabase rewrites the connection URL's database path to dbName, keeping
// the host (TCP or unix socket), credentials, and query params intact.
func withDatabase(t *testing.T, rawURL, dbName string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("testdb: parse POSTGRESQL_URL: %v", err)
	}
	u.Path = "/" + dbName
	return u.String()
}

// applyMigrations runs every db/migrations/*.sql file in order against dbURL.
func applyMigrations(t *testing.T, ctx context.Context, dbURL string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("testdb: connect for migrations: %v", err)
	}
	defer conn.Close(ctx)

	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("testdb: read migrations dir %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // sequential numbering => lexical order is apply order
	for _, name := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("testdb: read migration %s: %v", name, err)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("testdb: apply migration %s: %v", name, err)
		}
	}
}

// migrationsDir locates control-plane/db/migrations relative to this source
// file, so it resolves no matter which package's tests call New.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0) // .../control-plane/internal/testdb/testdb.go
	return filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations")
}

// Package store is the PostgreSQL persistence layer. It owns the pgx connection
// pool and the per-table query types. Higher layers depend on the small
// interfaces they need (e.g. api.UserStore), not on this package directly, so
// they stay testable without a database.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store holds the connection pool and the table stores built on it.
type Store struct {
	pool  *pgxpool.Pool
	Users *UserStore
}

// New opens a pgx pool against databaseURL and verifies connectivity with a
// ping. The caller owns the lifetime and must Close it.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool, Users: &UserStore{pool: pool}}, nil
}

// Close releases the pool's connections.
func (s *Store) Close() { s.pool.Close() }

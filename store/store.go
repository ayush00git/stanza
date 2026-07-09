// Package store is the Postgres-backed system of record for researcher profiles
// and resistance-design runs (feature 08, persistence). It replaces the
// process-local in-memory stores so run history survives a restart. The public
// surface is intentionally small: construct with New, apply schema with Migrate,
// then use the run and profile methods (in runs.go / profiles.go).
package store

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store owns a pgx connection pool. It is safe for concurrent use.
type Store struct {
	Pool *pgxpool.Pool
}

// New opens a pgx connection pool to databaseURL (a libpq/pgx connection string
// or URL) and verifies connectivity with a ping. The caller owns Close.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close releases the connection pool.
func (s *Store) Close() {
	if s != nil && s.Pool != nil {
		s.Pool.Close()
	}
}

// Migrate applies every embedded migration in filename order. Migrations use
// IF NOT EXISTS, so this is idempotent and safe to run at every startup.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", name, err)
		}
		if _, err := s.Pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("store: apply %s: %w", name, err)
		}
	}
	return nil
}

package outreachconn

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads and writes outreach_connection_values.
type Store struct{ pool *pgxpool.Pool }

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// All returns every saved value by key.
func (s *Store) All(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM outreach_connection_values`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Apply upserts every set entry and deletes every cleared key, in one
// transaction.
func (s *Store) Apply(ctx context.Context, set map[string]string, clear []string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	for k, v := range set {
		if _, err := tx.Exec(ctx, `
			INSERT INTO outreach_connection_values (key, value, updated_at) VALUES ($1, $2, $3)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
			k, v, now); err != nil {
			return err
		}
	}
	if len(clear) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM outreach_connection_values WHERE key = ANY($1)`, clear); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

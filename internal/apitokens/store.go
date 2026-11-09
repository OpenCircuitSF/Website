package apitokens

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Token is one api_tokens row. It never carries the secret.
type Token struct {
	ID         int64
	Name       string
	Prefix     string // display_prefix
	Scopes     []string
	CreatedBy  *int64
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Store is the Postgres store for api_tokens.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const tokenColumns = `id, name, display_prefix, scopes, created_by, created_at, last_used_at, revoked_at`

func scanToken(row pgx.Row) (Token, error) {
	var t Token
	err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.Scopes, &t.CreatedBy, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	return t, err
}

// Create generates a token, stores its hash and display prefix, and returns
// the row and the secret. The secret is returned once and never stored.
func (s *Store) Create(ctx context.Context, name string, scopes []string, createdBy *int64, now time.Time) (Token, string, error) {
	secret, err := Generate()
	if err != nil {
		return Token{}, "", fmt.Errorf("apitokens: generate: %w", err)
	}
	t := Token{
		Name:      name,
		Prefix:    secret[:DisplayPrefixLength],
		Scopes:    scopes,
		CreatedBy: createdBy,
		CreatedAt: now,
	}
	err = s.pool.QueryRow(ctx,
		`INSERT INTO api_tokens (name, token_hash, display_prefix, scopes, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		name, Hash(secret), t.Prefix, scopes, createdBy, now).Scan(&t.ID)
	if err != nil {
		return Token{}, "", fmt.Errorf("apitokens: create: %w", err)
	}
	return t, secret, nil
}

// List returns every token, active ones first, newest first.
func (s *Store) List(ctx context.Context) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tokenColumns+` FROM api_tokens
		ORDER BY (revoked_at IS NOT NULL), created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("apitokens: list: %w", err)
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("apitokens: list scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke marks a token revoked. It returns ErrAlreadyRevoked for a token
// already revoked and ErrNotFound for an unknown id.
func (s *Store) Revoke(ctx context.Context, id int64, now time.Time) (Token, error) {
	t, err := scanToken(s.pool.QueryRow(ctx,
		`UPDATE api_tokens SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL RETURNING `+tokenColumns,
		id, now))
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Token{}, fmt.Errorf("apitokens: revoke: %w", err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM api_tokens WHERE id = $1)`, id).Scan(&exists); err != nil {
		return Token{}, fmt.Errorf("apitokens: revoke lookup: %w", err)
	}
	if exists {
		return Token{}, ErrAlreadyRevoked
	}
	return Token{}, ErrNotFound
}

// Resolve looks up an active token by the hash of secret. A malformed,
// unknown or revoked token gives ErrNotFound. last_used_at is written at most
// once per LastUsedInterval.
func (s *Store) Resolve(ctx context.Context, secret string, now time.Time) (Token, error) {
	if !LooksLikeToken(secret) {
		return Token{}, ErrNotFound
	}
	t, err := scanToken(s.pool.QueryRow(ctx,
		`SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = $1 AND revoked_at IS NULL`,
		Hash(secret)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	if err != nil {
		return Token{}, fmt.Errorf("apitokens: resolve: %w", err)
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= LastUsedInterval {
		if _, err := s.pool.Exec(ctx,
			`UPDATE api_tokens SET last_used_at = $2 WHERE id = $1 AND (last_used_at IS NULL OR last_used_at <= $3)`,
			t.ID, now, now.Add(-LastUsedInterval)); err != nil {
			return Token{}, fmt.Errorf("apitokens: touch last_used_at: %w", err)
		}
		t.LastUsedAt = &now
	}
	return t, nil
}

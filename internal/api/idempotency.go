package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdempotencyTTL is how long an Idempotency-Key is remembered.
const IdempotencyTTL = 24 * time.Hour

// IdempotencyStore remembers Idempotency-Keys per API key
// (api_idempotency_keys, migrations/000054).
type IdempotencyStore interface {
	// Begin claims key for this request. (nil, nil) means it is new and the
	// caller runs the operation; a *StoredResponse is the earlier success to
	// replay. ErrIdempotencyMismatch and ErrIdempotencyInFlight are 409s.
	Begin(ctx context.Context, tokenID int64, key, op string, hash []byte, now time.Time) (*StoredResponse, error)
	// Complete stores the success response for key.
	Complete(ctx context.Context, tokenID int64, key string, status int, body []byte) error
	// Abandon forgets key, so a retry runs the operation again.
	Abandon(ctx context.Context, tokenID int64, key string) error
}

// StoredResponse is a remembered success.
type StoredResponse struct {
	Status int
	Body   []byte
}

var (
	// ErrIdempotencyMismatch: the key was used with a different request.
	ErrIdempotencyMismatch = errors.New("api: Idempotency-Key was used with a different request")
	// ErrIdempotencyInFlight: the first request with the key is still running.
	ErrIdempotencyInFlight = errors.New("api: a request with this Idempotency-Key is still in progress")
)

type pgIdempotencyStore struct{ pool *pgxpool.Pool }

// NewIdempotencyStore returns the Postgres IdempotencyStore. Expired keys are
// swept lazily by Begin, so no worker is needed.
func NewIdempotencyStore(pool *pgxpool.Pool) IdempotencyStore { return &pgIdempotencyStore{pool: pool} }

func (s *pgIdempotencyStore) Begin(ctx context.Context, tokenID int64, key, op string, hash []byte, now time.Time) (*StoredResponse, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM api_idempotency_keys WHERE created_at <= $1`, now.Add(-IdempotencyTTL)); err != nil {
		return nil, fmt.Errorf("api: idempotency sweep: %w", err)
	}
	var inserted int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_idempotency_keys (token_id, idem_key, operation, request_hash, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING
		RETURNING 1`, tokenID, key, op, hash, now).Scan(&inserted)
	if err == nil {
		return nil, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("api: idempotency insert: %w", err)
	}
	var (
		gotOp   string
		gotHash []byte
		status  *int
		body    []byte
	)
	err = s.pool.QueryRow(ctx, `
		SELECT operation, request_hash, status_code, response_body
		  FROM api_idempotency_keys WHERE token_id = $1 AND idem_key = $2`, tokenID, key).Scan(&gotOp, &gotHash, &status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		// Abandoned between the insert and the select: treat as in flight,
		// and the client's retry will claim it.
		return nil, ErrIdempotencyInFlight
	}
	if err != nil {
		return nil, fmt.Errorf("api: idempotency select: %w", err)
	}
	switch {
	case gotOp != op || !bytes.Equal(gotHash, hash):
		return nil, ErrIdempotencyMismatch
	case status == nil:
		return nil, ErrIdempotencyInFlight
	}
	return &StoredResponse{Status: *status, Body: body}, nil
}

func (s *pgIdempotencyStore) Complete(ctx context.Context, tokenID int64, key string, status int, body []byte) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE api_idempotency_keys SET status_code = $3, response_body = $4
		 WHERE token_id = $1 AND idem_key = $2`, tokenID, key, status, body); err != nil {
		return fmt.Errorf("api: idempotency complete: %w", err)
	}
	return nil
}

func (s *pgIdempotencyStore) Abandon(ctx context.Context, tokenID int64, key string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM api_idempotency_keys WHERE token_id = $1 AND idem_key = $2`, tokenID, key); err != nil {
		return fmt.Errorf("api: idempotency abandon: %w", err)
	}
	return nil
}

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// newIdemToken creates a throwaway token (never a seeded id, AGENTS.md §8b)
// and deletes it at cleanup, which cascades to its keys.
func newIdemToken(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	tok, _, err := apitokens.NewStore(pool).Create(context.Background(), "idem test", []string{apitokens.ScopeGuidePostsWrite}, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM api_tokens WHERE id = $1`, tok.ID) })
	return tok.ID
}

func hashOf(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func TestIdempotencyStoreBeginCompleteReplay(t *testing.T) {
	pool := testPool(t)
	s, ctx, id, now := NewIdempotencyStore(pool), context.Background(), newIdemToken(t, pool), time.Now()
	got, err := s.Begin(ctx, id, "k", "op", hashOf("a"), now)
	if err != nil || got != nil {
		t.Fatalf("first Begin: %v %v", got, err)
	}
	body := []byte(`{"b":1, "a":2}` + "\n")
	if err := s.Complete(ctx, id, "k", 201, body); err != nil {
		t.Fatal(err)
	}
	got, err = s.Begin(ctx, id, "k", "op", hashOf("a"), now.Add(time.Minute))
	if err != nil || got == nil || got.Status != 201 || !bytes.Equal(got.Body, body) {
		t.Fatalf("replay: %+v %v", got, err)
	}
}

func TestIdempotencyStoreMismatch(t *testing.T) {
	pool := testPool(t)
	s, ctx, id, now := NewIdempotencyStore(pool), context.Background(), newIdemToken(t, pool), time.Now()
	if _, err := s.Begin(ctx, id, "k", "op", hashOf("a"), now); err != nil {
		t.Fatal(err)
	}
	_ = s.Complete(ctx, id, "k", 200, []byte("{}"))
	if _, err := s.Begin(ctx, id, "k", "op", hashOf("b"), now); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("different hash: %v", err)
	}
	if _, err := s.Begin(ctx, id, "k", "other_op", hashOf("a"), now); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("different op: %v", err)
	}
}

func TestIdempotencyStoreInFlight(t *testing.T) {
	pool := testPool(t)
	s, ctx, id, now := NewIdempotencyStore(pool), context.Background(), newIdemToken(t, pool), time.Now()
	if _, err := s.Begin(ctx, id, "k", "op", hashOf("a"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(ctx, id, "k", "op", hashOf("a"), now); !errors.Is(err, ErrIdempotencyInFlight) {
		t.Fatalf("in flight: %v", err)
	}
}

func TestIdempotencyStoreAbandonAllowsRetry(t *testing.T) {
	pool := testPool(t)
	s, ctx, id, now := NewIdempotencyStore(pool), context.Background(), newIdemToken(t, pool), time.Now()
	_, _ = s.Begin(ctx, id, "k", "op", hashOf("a"), now)
	if err := s.Abandon(ctx, id, "k"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Begin(ctx, id, "k", "op", hashOf("a"), now); err != nil || got != nil {
		t.Fatalf("retry after abandon: %v %v", got, err)
	}
}

func TestIdempotencyStoreExpiresAfter24Hours(t *testing.T) {
	pool := testPool(t)
	s, ctx, id, now := NewIdempotencyStore(pool), context.Background(), newIdemToken(t, pool), time.Now()
	_, _ = s.Begin(ctx, id, "k", "op", hashOf("a"), now)
	_ = s.Complete(ctx, id, "k", 200, []byte("{}"))
	if got, _ := s.Begin(ctx, id, "k", "op", hashOf("a"), now.Add(IdempotencyTTL-time.Second)); got == nil {
		t.Fatal("key gone before 24 hours")
	}
	got, err := s.Begin(ctx, id, "k", "op", hashOf("b"), now.Add(IdempotencyTTL+time.Second))
	if err != nil || got != nil {
		t.Fatalf("after 24h+1s the key should be new: %v %v", got, err)
	}
}

func TestIdempotencyStoreScopedPerToken(t *testing.T) {
	pool := testPool(t)
	s, ctx, now := NewIdempotencyStore(pool), context.Background(), time.Now()
	a, b := newIdemToken(t, pool), newIdemToken(t, pool)
	_, _ = s.Begin(ctx, a, "k", "op", hashOf("a"), now)
	_ = s.Complete(ctx, a, "k", 200, []byte(`{"who":"a"}`))
	if got, err := s.Begin(ctx, b, "k", "op", hashOf("b"), now); err != nil || got != nil {
		t.Fatalf("same key, other token: %v %v", got, err)
	}
}

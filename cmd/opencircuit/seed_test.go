package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/db"
)

// TestSeedIdempotent exercises ensureAdminUser against a live PostgreSQL. It is
// gated on TEST_DATABASE_URL and skipped when unset so the default `go test`
// run needs no database. It runs the helper twice and confirms no duplicate
// admin row is created on the second run.
func TestSeedIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()

	const email = "seed-idempotency-test@example.com"

	// Clean up any residue from a previous run, and again at the end.
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
	}
	cleanup()
	defer cleanup()

	// First run.
	id1, err := ensureAdminUser(ctx, pool, email)
	if err != nil {
		t.Fatalf("ensureAdminUser (run 1): %v", err)
	}

	// Second run must not duplicate or error and must reuse the same row.
	id2, err := ensureAdminUser(ctx, pool, email)
	if err != nil {
		t.Fatalf("ensureAdminUser (run 2): %v", err)
	}

	if id1 != id2 {
		t.Fatalf("admin id changed between runs: %d != %d", id1, id2)
	}

	var userCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, email).Scan(&userCount); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 admin user, got %d", userCount)
	}
}

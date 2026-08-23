// Package testdb provides cross-process serialization for integration tests
// that share a single PostgreSQL test database (TEST_DATABASE_URL), a
// shared way to open the one connection pool a package's whole test binary
// should use (see Connect), and a source of unique test identifiers that
// does not depend on clock resolution (see Unique).
//
// `go test` runs each package's test binary concurrently. Several packages
// truncate the same shared tables in their setup, so concurrent runs corrupt
// each other's data. Each such package calls Lock from its TestMain to hold a
// PostgreSQL session-level advisory lock for the duration of its test run; only
// one package can hold the lock at a time, so they run one-at-a-time even under
// `go test ./...`. When TEST_DATABASE_URL is unset, the DB-backed tests skip
// themselves and Lock is a no-op.
package testdb

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey is an arbitrary fixed key shared by every package that
// serializes through this helper. All callers must use the same value.
const advisoryLockKey int64 = 0x53484F52544C4B // "SHORTLK"

// Lock acquires the shared advisory lock and returns a release function the
// caller must invoke before exiting (typically right before os.Exit). It blocks
// until the lock is free. If TEST_DATABASE_URL is unset, it returns a no-op
// release immediately.
func Lock() func() {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		return func() {}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		// Surface the problem but don't block the run: the DB-backed tests will
		// fail loudly on their own connection attempts.
		fmt.Fprintf(os.Stderr, "testdb: connect failed: %v\n", err)
		return func() {}
	}

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		fmt.Fprintf(os.Stderr, "testdb: advisory lock failed: %v\n", err)
		_ = conn.Close(ctx)
		return func() {}
	}

	return func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
		_ = conn.Close(ctx)
	}
}

// Connect opens the single pgxpool.Pool a DB-backed package's whole test
// binary should share, rather than the pre-#0091 pattern of one fresh pool
// per test.
//
// Call it once from TestMain, after Lock, and store the result in a
// package-level variable that every test's own pool helper returns; do not
// call it per test. `go test` runs each package as its own process, so "one
// package-level pool" falls out naturally from "call this once in TestMain"
// — no cross-process coordination is needed here, unlike Lock.
//
// Connect returns (nil, nil) when TEST_DATABASE_URL is unset. Callers must
// treat a nil pool as the signal to skip DB-backed tests (t.Skip), matching
// the pre-#0091 per-test behavior, rather than treating it as a connect
// failure.
func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		return nil, nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(connectCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("testdb: connect test db: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("testdb: ping test db: %w", err)
	}
	return pool, nil
}

// uniqueCounter backs Unique. It is process-wide (not per-package,
// per-test, or per-goroutine) and incremented atomically.
var uniqueCounter int64

// Unique returns an integer for building test-only identifiers (slugs,
// emails, titles, tokens) that must differ between calls — never a value
// meant to be read as a timestamp.
//
// It replaces the pattern of deriving "uniqueness" from
// time.Now().UnixNano() (#0158). That clock is not fine-grained enough on
// this project's hardware to serve as a uniqueness source: a 200,000-pair
// probe measured 189,484 identical consecutive readings — 94.7%, minimum
// delta 0ns — so two back-to-back calls building a slug from it are a near
// coin-flip for returning the same value, and any test that calls such a
// helper twice in a row (no intervening work) is a near coin-flip for a
// unique-constraint violation. An atomic counter has no such gap: each call
// is a distinct integer by construction, regardless of clock resolution.
//
// A bare counter is enough to make values distinct *within one process*,
// but is not on its own enough here. `go test ./...` runs each package as
// its own OS process (independent counters, each starting at 0), several of
// those processes point at the same shared TEST_DATABASE_URL, and more than
// one package mints identifiers from an identical literal prefix — e.g.
// both internal/interests and internal/handlers build interest slugs as
// "zz-test-<n>". Two such processes racing during `go test ./...` could
// each hand out counter value 1 and collide in the database even though
// neither process ever repeated a value itself. Folding in this process's
// PID (which no other concurrently running process can share) avoids that
// without requiring every call site to also thread through a package name
// or the ISSUE env var: the high 32 bits vary by process, the low 32 bits
// vary by call, and 32 bits of counter (~4 billion calls) is far more than
// any test binary will mint in a run.
func Unique() int64 {
	n := atomic.AddInt64(&uniqueCounter, 1)
	return int64(os.Getpid())<<32 | (n & 0xffffffff)
}

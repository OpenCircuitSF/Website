package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// readMigration000053 returns 000053's up and down SQL.
func readMigration000053(t *testing.T) (up, down string) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	upB, err := os.ReadFile(filepath.Join(dir, "000053_api_tokens_v2.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	downB, err := os.ReadFile(filepath.Join(dir, "000053_api_tokens_v2.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(upB), string(downB)
}

// scopesCheckArray matches the ADD CONSTRAINT api_tokens_scopes_check ...
// ARRAY[...] body; scopeLiteral matches each quoted element in it.
var (
	scopesCheckArray = regexp.MustCompile(`(?s)ADD CONSTRAINT api_tokens_scopes_check.*?ARRAY\[(.*?)\]`)
	scopeLiteral     = regexp.MustCompile(`'([^']*)'`)
)

// TestAPITokenScopesMatchMigration (#0674) asserts apitokens.KnownScopes and
// the scope array migrations/000053 writes into api_tokens_scopes_check hold
// exactly the same set, so a scope added on one side only fails here rather
// than as a 500 on create (Go-only) or an unusable scope (SQL-only).
func TestAPITokenScopesMatchMigration(t *testing.T) {
	up, _ := readMigration000053(t)
	m := scopesCheckArray.FindStringSubmatch(up)
	if m == nil {
		t.Fatal("000053 up: no ADD CONSTRAINT api_tokens_scopes_check ... ARRAY[...] found")
	}
	var sql []string
	for _, lit := range scopeLiteral.FindAllStringSubmatch(m[1], -1) {
		sql = append(sql, lit[1])
	}
	if len(sql) == 0 {
		t.Fatal("000053 up: the scopes array is empty")
	}
	goSet := slices.Clone(apitokens.KnownScopes)
	slices.Sort(goSet)
	slices.Sort(sql)
	if !slices.Equal(goSet, slices.Compact(slices.Clone(sql))) || len(sql) != len(goSet) {
		t.Fatalf("scope sets differ:\n Go         %v\n migration  %v", goSet, sql)
	}
}

// TestMigration000053_DownRefusesNewScopesAndRoundTrips runs 000053's down
// and up inside a rolled-back transaction: down refuses (check violation)
// while a token holds a scope 000053 added, and on a table holding only
// engagement:read tokens down then up both apply.
func TestMigration000053_DownRefusesNewScopesAndRoundTrips(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	up, down := readMigration000053(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	insert := `INSERT INTO api_tokens (name, token_hash, display_prefix, scopes)
		VALUES ('m53', decode(repeat('ab', 32), 'hex'), 'ocsf_m53test', $1)`

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, insert, []string{apitokens.ScopeEventsWrite}); err != nil {
		t.Fatalf("insert events:write token: %v", err)
	}
	_, err = tx.Exec(ctx, down)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("down with an events:write token: err %v, want check_violation 23514", err)
	}
	_ = tx.Rollback(ctx)

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	// Rows another package's tests left behind may hold new scopes; this
	// transaction is rolled back, so clearing them here is invisible outside.
	if _, err := tx.Exec(ctx, `DELETE FROM api_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, insert, []string{apitokens.ScopeEngagementRead}); err != nil {
		t.Fatalf("insert engagement:read token: %v", err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	var exp *time.Time
	if err := tx.QueryRow(ctx, `SELECT expires_at FROM api_tokens WHERE name = 'm53'`).Scan(&exp); err != nil || exp != nil {
		t.Fatalf("expires_at after round trip: %v err %v", exp, err)
	}
}

package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// readMigration000053 returns 000053's up and down SQL.
func readMigration000053(t *testing.T) (up, down string) {
	t.Helper()
	dir := migrationsDir
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

// scopesCheckArray matches a definition of api_tokens_scopes_check, inline in
// CREATE TABLE (000051) or by ADD CONSTRAINT (000053), up to its ARRAY[...]
// body; [^;] keeps a bare DROP CONSTRAINT from matching across statements.
// scopeLiteral matches each quoted element in the body.
var (
	scopesCheckArray = regexp.MustCompile(`(?s)CONSTRAINT api_tokens_scopes_check\b[^;]*?ARRAY\[(.*?)\]`)
	scopeLiteral     = regexp.MustCompile(`'([^']*)'`)
)

// latestScopesMigration returns the name and scope array of the
// highest-numbered *.up.sql in dir that defines api_tokens_scopes_check
// (#0682): the migration that defines the constraint as it stands, which a
// later widening replaces. Names sort by their zero-padded number.
func latestScopesMigration(dir string) (name string, scopes []string, err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return "", nil, err
	}
	slices.Sort(files)
	for i := len(files) - 1; i >= 0; i-- {
		b, err := os.ReadFile(files[i])
		if err != nil {
			return "", nil, err
		}
		m := scopesCheckArray.FindStringSubmatch(string(b))
		if m == nil {
			continue
		}
		for _, lit := range scopeLiteral.FindAllStringSubmatch(m[1], -1) {
			scopes = append(scopes, lit[1])
		}
		if len(scopes) == 0 {
			return "", nil, fmt.Errorf("%s: the api_tokens_scopes_check array is empty", filepath.Base(files[i]))
		}
		return filepath.Base(files[i]), scopes, nil
	}
	return "", nil, fmt.Errorf("no *.up.sql in %s defines api_tokens_scopes_check ... ARRAY[...]", dir)
}

// scopeSetsEqual reports whether sql holds exactly the scopes in goScopes,
// each once.
func scopeSetsEqual(goScopes, sql []string) bool {
	a, b := slices.Clone(goScopes), slices.Clone(sql)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b) && len(slices.Compact(slices.Clone(b))) == len(b)
}

// TestAPITokenScopesMatchMigration (#0674, #0682) asserts apitokens.KnownScopes
// and the scope array of the newest migration defining api_tokens_scopes_check
// hold exactly the same set, so a scope added on one side only fails here
// rather than as a 500 on create (Go-only) or an unusable scope (SQL-only).
func TestAPITokenScopesMatchMigration(t *testing.T) {
	name, sql, err := latestScopesMigration(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeSetsEqual(apitokens.KnownScopes, sql) {
		t.Fatalf("scope sets differ:\n Go          %v\n %s  %v", apitokens.KnownScopes, name, sql)
	}
}

// TestLatestScopesMigrationFollowsAWidening (#0682) proves the guard reads the
// newest definition: on a copy of migrations/ with a later migration that
// re-adds api_tokens_scopes_check with one more scope, the scan picks the new
// file and the parity check fails against today's KnownScopes. A file after it
// that does not touch the constraint is skipped.
func TestLatestScopesMigrationFollowsAWidening(t *testing.T) {
	src := migrationsDir
	dir := t.TempDir()
	files, err := filepath.Glob(filepath.Join(src, "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob %s: %v", src, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if name, sql, err := latestScopesMigration(dir); err != nil || !scopeSetsEqual(apitokens.KnownScopes, sql) {
		t.Fatalf("unmodified copy: %s %v %v", name, sql, err)
	}
	var quoted []string
	for _, s := range append(slices.Clone(apitokens.KnownScopes), "venues:write") {
		quoted = append(quoted, "'"+s+"'")
	}
	widen := "ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;\n" +
		"ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check\n" +
		"    CHECK (scopes <@ ARRAY[" + strings.Join(quoted, ", ") + "]::text[]);\n"
	if err := os.WriteFile(filepath.Join(dir, "000998_widen_scopes.up.sql"), []byte(widen), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000999_unrelated.up.sql"), []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, sql, err := latestScopesMigration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "000998_widen_scopes.up.sql" || !slices.Contains(sql, "venues:write") {
		t.Fatalf("scan picked %s %v, want 000998 with venues:write", name, sql)
	}
	if scopeSetsEqual(apitokens.KnownScopes, sql) {
		t.Fatal("parity check passed against a widened migration")
	}

	// The inline CREATE TABLE form is a definition too: with every file
	// after 000051 removed, the scan finds 000051's one-scope array.
	for _, f := range files {
		if filepath.Base(f) > "000051_create_api_tokens.up.sql" {
			if err := os.Remove(filepath.Join(dir, filepath.Base(f))); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, f := range []string{"000998_widen_scopes.up.sql", "000999_unrelated.up.sql"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	name, sql, err = latestScopesMigration(dir)
	if err != nil || name != "000051_create_api_tokens.up.sql" || !slices.Equal(sql, []string{apitokens.ScopeEngagementRead}) {
		t.Fatalf("truncated copy: %s %v %v, want 000051 [engagement:read]", name, sql, err)
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

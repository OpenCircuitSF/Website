package testdb

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lockDiagPool is the pool this file's DB-backed tests share, opened once
// here in TestMain rather than once per test (matching the pattern every
// other DB-backed package's TestMain uses — see internal/audit/main_test.go
// for the canonical shape). nil when TEST_DATABASE_URL is unset; every test
// below checks that via lockDiagPoolOrSkip and calls t.Skip.
//
// This is the only TestMain for package testdb's test binary — it also
// covers TestUnique_* in testdb_test.go, which do not need a database and
// run unaffected whether or not TEST_DATABASE_URL is set.
var lockDiagPool *pgxpool.Pool

// TestMain takes the same cross-package advisory lock every other DB-backed
// package's TestMain does, even though nothing here truncates a shared
// application table on entry the way those packages' own TestMain function
// does.
// TestCascadeClosure_MatchesPostgresNoticesAtRealCallSites does run a real
// `TRUNCATE ... CASCADE` against real application tables — but always
// inside a transaction it unconditionally rolls back — and briefly holds an
// ACCESS EXCLUSIVE lock on those tables while doing so. Taking the lock
// keeps that window from ever landing concurrently with another package's
// own entry truncate against the same shared database.
func TestMain(m *testing.M) {
	release := Lock()

	pool, err := Connect(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		release()
		os.Exit(1)
	}
	lockDiagPool = pool

	code := m.Run()

	if lockDiagPool != nil {
		lockDiagPool.Close()
	}
	release()
	os.Exit(code)
}

func lockDiagPoolOrSkip(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if lockDiagPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return lockDiagPool
}

// truncateCascadeNoticeRe extracts the relation name Postgres names in its
// own `NOTICE:  truncate cascades to table "X"` output for a real
// `TRUNCATE ... CASCADE`. This is the same signal #0097's review used by
// hand (issues/0097.md's Review notes) to independently confirm
// cascadeClosure's derived set was complete; here it is automated.
var truncateCascadeNoticeRe = regexp.MustCompile(`truncate cascades to table "([^"]+)"`)

// realTruncateCascadeClosure runs `TRUNCATE roots... CASCADE` against a
// dedicated connection configured to capture NOTICE messages, inside a
// transaction it always rolls back, and returns roots plus every relation
// named in a "truncate cascades to table" notice. It never commits, so it
// cannot actually remove data from a database another test or another
// agent is using — only the transaction's own view sees the truncate, and
// even that view is undone by the deferred rollback.
func realTruncateCascadeClosure(t *testing.T, roots []string) []string {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	var notices []string
	cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		notices = append(notices, n.Message)
	}

	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect for notice capture: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(roots, ", ")+" CASCADE"); err != nil {
		t.Fatalf("TRUNCATE %s CASCADE: %v", strings.Join(roots, ", "), err)
	}

	closure := append([]string{}, roots...)
	for _, msg := range notices {
		if m := truncateCascadeNoticeRe.FindStringSubmatch(msg); m != nil {
			closure = append(closure, m[1])
		}
	}
	return closure
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make(map[string]bool, len(a))
	for _, x := range a {
		sa[x] = true
	}
	for _, x := range b {
		if !sa[x] {
			return false
		}
	}
	return true
}

// TestCascadeClosure_MatchesPostgresNoticesAtRealCallSites is a regression
// guard for #0097's headline claim — cascadeClosure's derived set exactly
// equals what Postgres's own `TRUNCATE ... CASCADE` actually locks, at all
// four real EntryTruncate call sites (internal/audit, internal/mailing,
// internal/middleware, internal/workshops) — automating the by-hand check
// #0097's review did (issues/0097.md's Review notes). #0225 does not modify
// cascadeClosure at all, only diagnoseLockHolders' consumption of its
// output, so this exists to prove that non-modification didn't regress the
// thing it's adjacent to, not because #0225 touched this code path.
func TestCascadeClosure_MatchesPostgresNoticesAtRealCallSites(t *testing.T) {
	pool := lockDiagPoolOrSkip(t)

	cases := []struct {
		name  string
		roots []string
	}{
		{"internal/audit", []string{"audit_log", "users"}},
		{"internal/workshops", []string{"workshops"}},
		{"internal/mailing", []string{
			"email_sends", "campaign_interests", "email_campaigns",
			"subscriber_interests", "subscribers", "suppressions",
		}},
		{"internal/middleware", []string{
			"subscriber_interests", "subscribers",
			"sessions", "passkey_credentials", "audit_log", "users",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			closure, err := cascadeClosure(pool, tc.roots)
			if err != nil {
				t.Fatalf("cascadeClosure(%v): %v", tc.roots, err)
			}

			expected := realTruncateCascadeClosure(t, tc.roots)

			if !sameStringSet(closure, expected) {
				t.Fatalf("cascadeClosure(%v) = %v, but a real TRUNCATE ... CASCADE's own notices name %v",
					tc.roots, closure, expected)
			}
		})
	}
}

// TestDiagnoseLockHolders_DirectLockOnRealTable reproduces #0097's review's
// "direct-lock case" (issues/0097.md's Review notes: "Same setup, lock on
// audit_log itself... diagnostic named audit_log and pid ... correctly") —
// proving #0225's rewrite of diagnoseLockHolders' matching still finds a
// lock on a literally-named, unqualified, public-schema table (the common
// case, and the only shape the old relname-based match ever actually had to
// handle in this schema).
func TestDiagnoseLockHolders_DirectLockOnRealTable(t *testing.T) {
	pool := lockDiagPoolOrSkip(t)
	ctx := context.Background()

	holderConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holderConn.Release()

	tx, err := holderConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var holderPID int32
	if err := tx.QueryRow(ctx, "select pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE audit_log IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("LOCK TABLE audit_log: %v", err)
	}

	diag := diagnoseLockHolders(pool, []string{"audit_log"})
	if !strings.Contains(diag, "audit_log") {
		t.Fatalf("diagnoseLockHolders did not name audit_log; got:\n%s", diag)
	}
	if !strings.Contains(diag, fmt.Sprintf("pid %d ", holderPID)) {
		t.Fatalf("diagnoseLockHolders did not name the actual lock-holder pid %d; got:\n%s", holderPID, diag)
	}
}

// TestDiagnoseLockHolders_CascadeTargetLock reproduces #0097's review's
// original bug report exactly: a lock held on `sessions` — a relation
// reached only via CASCADE from internal/audit's literal `audit_log, users`
// roots, never named directly — which the pre-#0097 diagnostic missed
// entirely and reported (wrongly) as "not a lock wait at all"
// (issues/0097.md's Description). #0097 fixed that by deriving the closure
// from pg_constraint; this proves #0225's rewrite of the matching side
// didn't quietly reopen it.
func TestDiagnoseLockHolders_CascadeTargetLock(t *testing.T) {
	pool := lockDiagPoolOrSkip(t)
	ctx := context.Background()

	closure, err := cascadeClosure(pool, []string{"audit_log", "users"})
	if err != nil {
		t.Fatalf("cascadeClosure: %v", err)
	}

	holderConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holderConn.Release()

	tx, err := holderConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var holderPID int32
	if err := tx.QueryRow(ctx, "select pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE sessions IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("LOCK TABLE sessions: %v", err)
	}

	diag := diagnoseLockHolders(pool, closure)
	if !strings.Contains(diag, "sessions") {
		t.Fatalf("diagnoseLockHolders did not name the cascade-target sessions; got:\n%s", diag)
	}
	if !strings.Contains(diag, fmt.Sprintf("pid %d ", holderPID)) {
		t.Fatalf("diagnoseLockHolders did not name the actual lock-holder pid %d; got:\n%s", holderPID, diag)
	}
}

// TestDiagnoseLockHolders_SchemaQualifiedOutsideSearchPath is #0225's core
// reproduction: cascadeClosure formats a relation outside search_path as a
// schema-qualified `schema.table` string (conrelid::regclass::text's own
// behavior), and the pre-#0225 diagnostic matched against bare
// pg_class.relname — which never carries a schema qualifier — so this case
// always missed. This creates a real relation outside search_path, takes a
// real ACCESS EXCLUSIVE lock on it, and hands diagnoseLockHolders exactly
// the string cascadeClosure would produce for it.
func TestDiagnoseLockHolders_SchemaQualifiedOutsideSearchPath(t *testing.T) {
	pool := lockDiagPoolOrSkip(t)
	ctx := context.Background()

	schema := fmt.Sprintf("zz_testdb_0225_%d", Unique())
	qualified := schema + ".child"

	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	if _, err := pool.Exec(ctx, "CREATE TABLE "+qualified+" (id int)"); err != nil {
		t.Fatalf("create table %s: %v", qualified, err)
	}

	// Confirm what cascadeClosure-style formatting actually produces for
	// this relation, rather than assuming it — this is the exact string
	// diagnoseLockHolders is handed in production, via
	// EntryTruncate -> cascadeClosure.
	var formatted string
	if err := pool.QueryRow(ctx, "select $1::regclass::text", qualified).Scan(&formatted); err != nil {
		t.Fatalf("format regclass: %v", err)
	}
	if !strings.Contains(formatted, schema) {
		t.Fatalf("expected the formatted identity to be schema-qualified (relation is outside search_path), got %q", formatted)
	}

	holderConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holderConn.Release()

	tx, err := holderConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var holderPID int32
	if err := tx.QueryRow(ctx, "select pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE "+qualified+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("LOCK TABLE %s: %v", qualified, err)
	}

	diag := diagnoseLockHolders(pool, []string{formatted})
	if !strings.Contains(diag, formatted) {
		t.Fatalf("diagnoseLockHolders did not find/name the schema-qualified relation %q; got:\n%s", formatted, diag)
	}
	if !strings.Contains(diag, fmt.Sprintf("pid %d ", holderPID)) {
		t.Fatalf("diagnoseLockHolders did not name the actual lock-holder pid %d; got:\n%s", holderPID, diag)
	}
}

// TestDiagnoseLockHolders_QuotedMixedCaseRelation is #0225's other named
// reproduction: cascadeClosure quotes a relation name that needs it (any
// name that isn't all-lowercase, or that collides with a keyword, etc.),
// and bare pg_class.relname is never quoted — a naive string comparison of
// the two never agrees. Also proves the converse within the same test: the
// unquoted, lowercase-folded spelling of the same characters must NOT
// match, since it names a different (here: nonexistent) relation.
func TestDiagnoseLockHolders_QuotedMixedCaseRelation(t *testing.T) {
	pool := lockDiagPoolOrSkip(t)
	ctx := context.Background()

	table := fmt.Sprintf("ZzTestdb0225Mixed%d", Unique())
	quoted := `"` + table + `"`

	if _, err := pool.Exec(ctx, "CREATE TABLE "+quoted+" (id int)"); err != nil {
		t.Fatalf("create table %s: %v", quoted, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+quoted)
	})

	var formatted string
	if err := pool.QueryRow(ctx, "select $1::regclass::text", quoted).Scan(&formatted); err != nil {
		t.Fatalf("format regclass: %v", err)
	}
	if formatted != quoted {
		t.Fatalf("expected the formatted identity to be the quoted mixed-case name %q, got %q", quoted, formatted)
	}

	holderConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holderConn.Release()

	tx, err := holderConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var holderPID int32
	if err := tx.QueryRow(ctx, "select pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("pg_backend_pid: %v", err)
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE "+quoted+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("LOCK TABLE %s: %v", quoted, err)
	}

	diag := diagnoseLockHolders(pool, []string{formatted})
	if !strings.Contains(diag, formatted) {
		t.Fatalf("diagnoseLockHolders did not find/name the quoted mixed-case relation %s; got:\n%s", formatted, diag)
	}
	if !strings.Contains(diag, fmt.Sprintf("pid %d ", holderPID)) {
		t.Fatalf("diagnoseLockHolders did not name the actual lock-holder pid %d; got:\n%s", holderPID, diag)
	}

	// Converse: the unquoted, lowercase-folded spelling names a different
	// (nonexistent) relation and must not false-match the quoted one.
	lower := strings.ToLower(table)
	diagLower := diagnoseLockHolders(pool, []string{lower})
	if diagLower != "" {
		t.Fatalf("diagnoseLockHolders matched the unquoted lowercase spelling %q against the quoted mixed-case relation %s; got:\n%s",
			lower, quoted, diagLower)
	}
}

// TestDiagnoseLockHolders_SameNameDifferentSchema_NoFalsePositive is the
// converse #0225 asks for explicitly: a same-named relation in a different
// schema must not be falsely reported as the one actually locked. A
// text-based match on bare relname (the pre-#0225 behavior) cannot tell
// these apart at all — both schemas' copies share one relname — so if this
// test only proved the positive case above, an implementation that matched
// by relname alone would still pass it.
func TestDiagnoseLockHolders_SameNameDifferentSchema_NoFalsePositive(t *testing.T) {
	pool := lockDiagPoolOrSkip(t)
	ctx := context.Background()

	suffix := Unique()
	schemaLocked := fmt.Sprintf("zz_testdb_0225_locked_%d", suffix)
	schemaOther := fmt.Sprintf("zz_testdb_0225_other_%d", suffix)
	const table = "shared_name"

	for _, s := range []string{schemaLocked, schemaOther} {
		if _, err := pool.Exec(ctx, "CREATE SCHEMA "+s); err != nil {
			t.Fatalf("create schema %s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, "DROP SCHEMA "+schemaLocked+" CASCADE")
		_, _ = pool.Exec(bg, "DROP SCHEMA "+schemaOther+" CASCADE")
	})
	for _, s := range []string{schemaLocked, schemaOther} {
		if _, err := pool.Exec(ctx, "CREATE TABLE "+s+"."+table+" (id int)"); err != nil {
			t.Fatalf("create table %s.%s: %v", s, table, err)
		}
	}

	var formattedLocked, formattedOther string
	if err := pool.QueryRow(ctx, "select $1::regclass::text", schemaLocked+"."+table).Scan(&formattedLocked); err != nil {
		t.Fatalf("format regclass (locked): %v", err)
	}
	if err := pool.QueryRow(ctx, "select $1::regclass::text", schemaOther+"."+table).Scan(&formattedOther); err != nil {
		t.Fatalf("format regclass (other): %v", err)
	}

	holderConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire holder conn: %v", err)
	}
	defer holderConn.Release()

	tx, err := holderConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock ONLY schemaLocked's copy.
	if _, err := tx.Exec(ctx, "LOCK TABLE "+schemaLocked+"."+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("LOCK TABLE %s.%s: %v", schemaLocked, table, err)
	}

	// Asking about schemaOther's copy (the one NOT locked) must find
	// nothing — the converse case.
	diag := diagnoseLockHolders(pool, []string{formattedOther})
	if diag != "" {
		t.Fatalf("diagnoseLockHolders falsely reported a lock on %s (not locked) — matched the same-named relation in %s instead; got:\n%s",
			formattedOther, schemaLocked, diag)
	}

	// Sanity: asking about schemaLocked's copy (the one actually locked)
	// DOES find it, proving the empty result above is a real converse case
	// and not just "the query never works here."
	diagLocked := diagnoseLockHolders(pool, []string{formattedLocked})
	if !strings.Contains(diagLocked, formattedLocked) {
		t.Fatalf("diagnoseLockHolders did not find the actually-locked %s; got:\n%s", formattedLocked, diagLocked)
	}
}

package audit

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testDBPool is the single connection pool shared by every DB-backed test in
// this package, opened once here rather than once per test (#0091). It is
// nil when TEST_DATABASE_URL is unset; auditTestPool checks that and skips.
var testDBPool *pgxpool.Pool

// TestMain serializes this package's live-DB tests against the other DB-backed
// packages via the shared advisory lock, since they all truncate the same
// shared tables. A no-op when TEST_DATABASE_URL is unset.
func TestMain(m *testing.M) {
	release := testdb.Lock()

	pool, err := testdb.Connect(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		release()
		os.Exit(1)
	}
	testDBPool = pool

	// #0091 round two: auditTestPool used to TRUNCATE audit_log/users on
	// every call. It no longer does — every seedUser call in this package
	// now takes a per-test-unique email (see audit_test.go), so the tables
	// only need to start clean once, here, not before every test.
	//
	// #0097 item 2: this used to carry its own fixed 10s deadline via a
	// local context.WithTimeout, a second differently-valued bound
	// alongside the 20s internal/handlers and cmd/opencircuit settled on
	// (#0084). testdb.EntryTruncate centralizes the constant and adds
	// lock-holder diagnosis on failure (#0097 item 3). The tables argument
	// below names only what this statement truncates literally — CASCADE
	// closure (e.g. users -> email_campaigns via its created_by FK, which
	// carries no ON DELETE clause at all) is derived by EntryTruncate itself
	// from pg_constraint; do not hand-add relations here (#0097 item 3
	// review: a hand-written attempt at this list missed 6).
	if testDBPool != nil {
		testdb.EntryTruncate(testDBPool, release,
			`TRUNCATE audit_log, users RESTART IDENTITY CASCADE`,
			[]string{"audit_log", "users"})
	}

	code := m.Run()

	if testDBPool != nil {
		testDBPool.Close()
	}
	release()
	os.Exit(code)
}

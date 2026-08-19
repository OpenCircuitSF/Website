package audit

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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
	if testDBPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, truncErr := testDBPool.Exec(ctx, `TRUNCATE audit_log, users RESTART IDENTITY CASCADE`)
		cancel()
		if truncErr != nil {
			fmt.Fprintf(os.Stderr, "testdb: entry truncate failed: %v\n", truncErr)
			testDBPool.Close()
			release()
			os.Exit(1)
		}
	}

	code := m.Run()

	if testDBPool != nil {
		testDBPool.Close()
	}
	release()
	os.Exit(code)
}

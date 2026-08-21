package sesnotify

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testDBPool is the single connection pool shared by every DB-backed test in
// this package, opened once here rather than once per test (#0091's
// pattern, applied fresh — this package has no DB-backed tests before
// #0038). It is nil when TEST_DATABASE_URL is unset; testPool checks that
// and skips.
var testDBPool *pgxpool.Pool

// TestMain serializes this package's live-DB tests against the other
// DB-backed packages via testdb's shared advisory lock. A no-op when
// TEST_DATABASE_URL is unset. email_events is never truncated here —
// store_test.go seeds every row under a unique sns_message_id (see
// uniqueSNSMessageID), so no two tests' rows can collide and the table
// never needs to start clean.
func TestMain(m *testing.M) {
	release := testdb.Lock()

	pool, err := testdb.Connect(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		release()
		os.Exit(1)
	}
	testDBPool = pool

	code := m.Run()

	if testDBPool != nil {
		testDBPool.Close()
	}
	release()
	os.Exit(code)
}

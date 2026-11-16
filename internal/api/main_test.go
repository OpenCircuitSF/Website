package api

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testDBPool is the one pool shared by this package's DB-backed tests (the
// idempotency store). It is nil when TEST_DATABASE_URL is unset; testPool
// skips then. Everything else here runs without a database.
var testDBPool *pgxpool.Pool

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

// testPool returns the shared pool, skipping when there is no database.
// Tests create their own tokens; deleting them cascades to their keys.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return testDBPool
}

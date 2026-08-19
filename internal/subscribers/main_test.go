package subscribers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testDBPool is the single connection pool shared by every DB-backed test in
// this package, opened once here rather than once per test (#0091 — 50
// tests each building their own pgxpool.Pool and truncating twice pushed the
// package past two minutes, and past Go's 10-minute per-package timeout
// under load). It is nil when TEST_DATABASE_URL is unset; testPool (in
// store_test.go) checks that and skips.
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

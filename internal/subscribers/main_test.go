package subscribers

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

	// #0091 round two: the per-test TRUNCATE that survived the first pass
	// (see store_test.go's testPool) was itself the dominant remaining cost
	// (~1.4s/test on an idle machine, 71.9s total for 50 tests). Every test
	// in this package already seeds its own rows through uniqueEmail(t), so
	// no two tests' rows ever collide — truncating once, here, before any
	// test runs, is enough to guarantee a clean starting state without
	// paying a TRUNCATE per test. See testPool's doc comment for the
	// isolation argument in full, and TestIsolation_UniqueDataNeverCollides
	// for the proof.
	if testDBPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, truncErr := testDBPool.Exec(ctx,
			`TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`)
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

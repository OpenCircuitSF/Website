package workshops

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
// this package, opened once here rather than once per test — mirrors
// internal/interests/main_test.go and internal/mailing/main_test.go (#0091).
// It is nil when TEST_DATABASE_URL is unset; testPool checks that and skips.
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

	// Entry truncate, mirroring internal/mailing/main_test.go: workshops has
	// no seed data (unlike interests' twelve-row taxonomy), so it is always
	// safe to start this package's suite from an empty table rather than
	// accumulating rows across runs against a shared (per-agent) test
	// database. workshop_interests cascades via migration 000020's
	// ON DELETE CASCADE.
	if testDBPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, truncErr := testDBPool.Exec(ctx, `TRUNCATE workshops RESTART IDENTITY CASCADE`)
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

// testPool returns the shared pool or skips if TEST_DATABASE_URL is unset.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

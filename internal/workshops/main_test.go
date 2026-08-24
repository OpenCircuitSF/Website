package workshops

import (
	"context"
	"fmt"
	"os"
	"testing"

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
	// ON DELETE CASCADE, so it is listed alongside workshops below even
	// though it is never named in the TRUNCATE statement itself — CASCADE
	// locks it too, and #0097 item 3's diagnosis needs to know to look
	// there.
	//
	// #0097 item 2: this used to carry its own fixed 10s deadline via a
	// local context.WithTimeout, a second differently-valued bound
	// alongside the 20s internal/handlers and cmd/opencircuit settled on
	// (#0084). testdb.EntryTruncate centralizes the constant and adds
	// lock-holder diagnosis on failure (#0097 item 3).
	if testDBPool != nil {
		testdb.EntryTruncate(testDBPool, release,
			`TRUNCATE workshops RESTART IDENTITY CASCADE`,
			[]string{"workshops", "workshop_interests"})
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

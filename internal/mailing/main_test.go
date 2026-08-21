package mailing

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
// internal/subscribers/main_test.go and internal/handlers/main_test.go
// (#0091). It is nil when TEST_DATABASE_URL is unset; testPool (below)
// checks that and skips.
//
// This is internal/mailing's first DB-backed test file (#0041's
// CampaignStore). #0044's plan (issues/0044.md §10) names the same TestMain
// shape for its own AudienceStore tests, including subscribers/suppressions
// in the entry truncate — when #0044 lands, it should EXTEND the truncate
// list below rather than add a second TestMain (a package may have only
// one).
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

	// Only the three campaign tables this issue's tests touch. #0044 will
	// extend this to include subscriber_interests, subscribers, and
	// suppressions once its AudienceStore tests need them (its plan already
	// names the exact five-table list).
	if testDBPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, truncErr := testDBPool.Exec(ctx,
			`TRUNCATE email_sends, campaign_interests, email_campaigns RESTART IDENTITY CASCADE`)
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

package mailing

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

	// Extended by #0044 (AudienceStore) to the full five-table list its plan
	// names: subscriber_interests, subscribers, and suppressions join the
	// original three campaign tables, since audience_test.go seeds and reads
	// across all of them. Still a single TestMain per this package's own
	// "extend, don't add a second one" rule above.
	// #0097 item 2: this used to carry its own fixed 10s deadline via a
	// local context.WithTimeout, a second differently-valued bound
	// alongside the 20s internal/handlers and cmd/opencircuit settled on
	// (#0084). testdb.EntryTruncate centralizes the constant and adds
	// lock-holder diagnosis on failure (#0097 item 3). The tables argument
	// below names what this statement truncates literally; EntryTruncate
	// derives any further CASCADE closure itself from pg_constraint, so
	// this list does not need hand maintenance as the schema grows.
	if testDBPool != nil {
		testdb.EntryTruncate(testDBPool, release,
			`TRUNCATE email_sends, campaign_interests, email_campaigns, subscriber_interests, subscribers, suppressions RESTART IDENTITY CASCADE`,
			[]string{"email_sends", "campaign_interests", "email_campaigns", "subscriber_interests", "subscribers", "suppressions"})
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

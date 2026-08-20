package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testDBPool is the single connection pool shared by every DB-backed test in
// this package, opened once here rather than once per test (#0091 — this
// package's six separate per-file pool helpers, each building a fresh
// pgxpool.Pool and truncating twice per test, pushed the package to 622s,
// well past Go's 10-minute default per-package timeout under load). It is
// nil when TEST_DATABASE_URL is unset; each of this package's *TestPool
// helpers (credsTestPool, subscribeTestPool, adminSubscribersTestPool,
// settingsTestPool, interestsTestPool, journeyTestPool) checks that and
// skips.
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

	// #0091 round two: subscribeTestPool (subscribe_test.go) and
	// journeyTestPool (journey_testutil_test.go) used to TRUNCATE
	// subscriber_interests/subscribers/audit_log on every call — the
	// dominant remaining cost once the pool itself became shared (see
	// issues/0091.md). Both groups now seed exclusively through
	// subscribeUniqueEmail(t)/journeyUniqueEmail(t), so no two tests' rows
	// collide and the tables only need to start clean once, here, before
	// the first test.
	//
	// credsTestPool, settingsTestPool, interestsTestPool and
	// adminSubscribersTestPool still truncate their own table sets per
	// test (unchanged by #0091 round two — see issues/0091.md's Work log
	// for why that group was left as-is), so this entry truncate only
	// needs to cover the tables those groups don't already guarantee clean
	// before their own first test.
	if testDBPool != nil {
		// handlersDBOpTimeout (credentials_test.go, #0084): this used to be
		// a separately-valued flat 10s, a second arbitrary bound alongside
		// truncateCredsTables' flat 5s (#0097 item 2) for what is the same
		// class of operation — a single TRUNCATE whose duration depends on
		// contention, not on what it does. Unified onto the one documented
		// constant instead of carrying two different numbers for the same
		// kind of statement. A failure here calls os.Exit(1) for the whole
		// test binary (see below), so getting this bound right matters more
		// than most: a false failure here fails every test in the package,
		// not just one.
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		_, truncErr := testDBPool.Exec(ctx,
			`TRUNCATE subscriber_interests, subscribers, audit_log RESTART IDENTITY CASCADE`)
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

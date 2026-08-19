package middleware

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
// nil when TEST_DATABASE_URL is unset; the package's testPool/signupTestPool
// helpers check that and skip.
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

	// #0091 round two: testPool (auth_test.go) and signupTestPool
	// (clientip_signup_test.go) used to TRUNCATE their respective tables on
	// every call. Both suites now seed rows under a per-test-unique value
	// (uniqueAuthEmail/uniqueSessionToken, uniqueSignupEmail) instead, so the
	// tables only need to start clean once, here, before any test runs.
	if testDBPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, truncErr := testDBPool.Exec(ctx,
			`TRUNCATE subscriber_interests, subscribers,
			          sessions, passkey_credentials, audit_log, users
			 RESTART IDENTITY CASCADE`)
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

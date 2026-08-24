package middleware

import (
	"context"
	"fmt"
	"os"
	"testing"

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
	// #0097 item 2: this used to carry its own fixed 10s deadline via a
	// local context.WithTimeout, a second differently-valued bound
	// alongside the 20s internal/handlers and cmd/opencircuit settled on
	// (#0084). testdb.EntryTruncate centralizes the constant and adds
	// lock-holder diagnosis on failure (#0097 item 3). The tables argument
	// below names only what this statement truncates literally — CASCADE
	// closure (e.g. sessions/passkey_credentials/webauthn_challenges from
	// users, email_campaigns from users' created_by FK) is derived by
	// EntryTruncate itself from pg_constraint; do not hand-add relations
	// here (#0097 item 3 review: a hand-written attempt at this list
	// missed 4).
	if testDBPool != nil {
		testdb.EntryTruncate(testDBPool, release,
			`TRUNCATE subscriber_interests, subscribers,
			          sessions, passkey_credentials, audit_log, users
			 RESTART IDENTITY CASCADE`,
			[]string{"subscriber_interests", "subscribers", "sessions", "passkey_credentials", "audit_log", "users"})
	}

	code := m.Run()

	if testDBPool != nil {
		testDBPool.Close()
	}
	release()
	os.Exit(code)
}

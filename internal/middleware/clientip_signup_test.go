package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// signupTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset, truncating the
// subscribers tables on entry only so the test starts from a clean slate.
// Mirrors the helper in internal/subscribers/store_test.go; this package
// already serializes DB-backed test packages via testdb.Lock in
// main_test.go, so truncating a table another concurrently-running package
// also owns is safe — only one package holds the advisory lock at a time.
func signupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateSubscribers(t, testDBPool)
	return testDBPool
}

func truncateSubscribers(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate subscriber tables: %v", err)
	}
}

// TestSignupSurvivesUnparseableXFF is #0080's acceptance test. Before the
// fix, ClientIP returned the rightmost X-Forwarded-For entry verbatim with
// no validation that it was even an IP address. "not-an-ip" then went
// straight into subscribers.Store.Create's SignupIP, which is an INET
// column: the INSERT failed, #0026's handler still returned its uniform 202
// regardless (the whole security property), and the signup silently never
// happened — a person saw success and was not subscribed.
//
// The fix: ClientIP must net.ParseIP the trusted rightmost entry and fall
// back to the peer address when it doesn't parse. This test proves the
// fallback all the way through to a real subscriber row existing.
func TestSignupSurvivesUnparseableXFF(t *testing.T) {
	pool := signupTestPool(t)
	store := subscribers.NewStore(pool)

	r := req("127.0.0.1:9999", "1.1.1.1, not-an-ip")
	ip := ClientIP(r)
	if ip != "127.0.0.1" {
		t.Fatalf("ClientIP = %q, want fallback peer %q for an unparseable rightmost entry", ip, "127.0.0.1")
	}

	sub, err := store.Create(context.Background(), subscribers.NewSignup{
		Email:      uniqueSignupEmail(),
		SignupIP:   ip,
		ConfirmTTL: 24 * time.Hour,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Create: %v (the signup was silently dropped)", err)
	}
	if sub.SignupIP == nil || *sub.SignupIP != "127.0.0.1" {
		t.Errorf("SignupIP = %v, want the fallback peer %q", sub.SignupIP, "127.0.0.1")
	}
}

// TestSignupSurvivesPortOnRightmostEntry is the port-on-the-rightmost-entry
// shape from the issue: "203.0.113.5:4444" must not be stored verbatim
// (INET has no port syntax, so that would also drop the signup). The fix
// strips the port and trusts the bare IP.
func TestSignupSurvivesPortOnRightmostEntry(t *testing.T) {
	pool := signupTestPool(t)
	store := subscribers.NewStore(pool)

	r := req("127.0.0.1:9999", "198.51.100.9, 203.0.113.5:4444")
	ip := ClientIP(r)
	if ip != "203.0.113.5" {
		t.Fatalf("ClientIP = %q, want port stripped to %q", ip, "203.0.113.5")
	}

	sub, err := store.Create(context.Background(), subscribers.NewSignup{
		Email:      uniqueSignupEmail(),
		SignupIP:   ip,
		ConfirmTTL: 24 * time.Hour,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Create: %v (the signup was silently dropped)", err)
	}
	if sub.SignupIP == nil || *sub.SignupIP != "203.0.113.5" {
		t.Errorf("SignupIP = %v, want %q (port stripped, not stored verbatim)", sub.SignupIP, "203.0.113.5")
	}
}

func uniqueSignupEmail() string {
	return "zz-clientip-" + time.Now().UTC().Format("20060102T150405.000000000") + "@example.com"
}

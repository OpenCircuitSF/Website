package middleware

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// signupTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset.
//
// #0091 round two: this used to TRUNCATE the subscribers tables on every
// call. It no longer does: both tests here already seed through
// uniqueSignupEmail(), so no two tests' rows collide, and the table only
// needs to start clean once — done in main_test.go's TestMain — not before
// every test.
func signupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
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

// uniqueSignupEmail is the live defect #0163 fixes: unlike the file's other
// helpers it takes no label, so the clock used to be its *only*
// discriminator, and the two tests above call it separated by nothing but
// a database insert — exactly the "masked by intervening round trips until
// it isn't" shape #0158's review flagged.
func uniqueSignupEmail() string {
	return fmt.Sprintf("zz-clientip-%d@example.com", testdb.Unique())
}

// TestUniqueSignupEmail_NeverCollidesAcrossManyBackToBackCalls is a plain
// unit test (no database) guarding #0163's fix to the live defect in this
// file: uniqueSignupEmail takes no label, so before the fix the formatted
// wall clock was its *only* discriminator, and TestSignupSurvivesUnparseableXFF
// / TestSignupSurvivesPortOnRightmostEntry above call it separated by
// nothing but a database insert.
func TestUniqueSignupEmail_NeverCollidesAcrossManyBackToBackCalls(t *testing.T) {
	const n = 20000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		email := uniqueSignupEmail()
		if _, dup := seen[email]; dup {
			t.Fatalf("call %d: uniqueSignupEmail returned a duplicate address %q", i, email)
		}
		seen[email] = struct{}{}
	}
}

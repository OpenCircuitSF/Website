package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// auditTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset.
//
// #0091 round two: this used to TRUNCATE audit_log/users on every call. It
// no longer does — seedUser now takes a per-test-unique email (see below),
// so the tables only need to start clean once, done in TestMain.
func auditTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

// seedUserEmail builds the unique email seedUser inserts. Split out from
// seedUser (#0163 review pass 2) so the identifier construction can be
// guarded on its own, with no database in the loop — the DB round trip
// seedUser performs between two calls was masking the very defect the
// original guard was meant to catch (see
// TestSeedUser_BackToBackCallsNeverCollide's doc comment below).
//
// #0163: the "-<ts>" suffix used to be time.Now() formatted to
// nanosecond-looking precision, the same repeating-clock defect #0158
// fixed elsewhere in other helpers, just rendered as a string.
func seedUserEmail(label string) string {
	return fmt.Sprintf("zz-audit-%s-%d@example.com", label, testdb.Unique())
}

// seedUser inserts an active account and returns its id, so an entry's
// actor_id/user_id can satisfy the FK constraint. label becomes a prefix of
// a generated unique email, not the literal address: #0091 round two
// stopped truncating users between tests, so a literal like
// "actor@example.com" (fine when every test started from an empty table)
// would collide with the same test's own row on a second `-count=2`
// iteration, and no assertion in this file depends on the exact address,
// only on the returned id.
func seedUser(t *testing.T, pool *pgxpool.Pool, label string) int64 {
	t.Helper()
	email := seedUserEmail(label)
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, is_admin, active, created_at)
		 VALUES ($1, FALSE, TRUE, now()) RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

// TestSeedUserEmail_NeverCollidesAcrossManyBackToBackCalls is the real guard
// for #0163's fix to seedUser. It loops the identifier expression itself,
// with no database in the loop, in the shape
// internal/middleware/auth_test.go's TestUniqueAuthEmail_... and
// TestUniqueSessionToken_... use.
//
// #0163 review pass 1 bounced a DB-backed two-call variant of this test
// (see TestSeedUser_BackToBackCallsNeverCollide below) as vacuous: measured
// on this machine, the old formatted-clock construction repeated on
// 1545/2000 (77.2%) of truly adjacent reads, but 0/200 (0.0%) of reads
// separated by a single INSERT round trip — and seedUser always performs an
// INSERT between its two identifier constructions, so a two-call DB guard
// can never observe the defect it names. This test removes the round trip
// instead of adding more calls around it.
func TestSeedUserEmail_NeverCollidesAcrossManyBackToBackCalls(t *testing.T) {
	const n = 20000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		email := seedUserEmail("same-label")
		if _, dup := seen[email]; dup {
			t.Fatalf("call %d: seedUserEmail returned a duplicate address %q", i, email)
		}
		seen[email] = struct{}{}
	}
}

// TestSeedUser_BackToBackCallsNeverCollide is a secondary, DB-backed
// integration check that seedUser's INSERT actually succeeds twice in a
// row against the real users.email UNIQUE constraint (migrations/000001).
// It is not the guard against #0163's defect — see
// TestSeedUserEmail_NeverCollidesAcrossManyBackToBackCalls's doc comment for
// why a two-call, round-trip-separated test cannot detect that regression.
func TestSeedUser_BackToBackCallsNeverCollide(t *testing.T) {
	pool := auditTestPool(t)
	id1 := seedUser(t, pool, "b2b")
	id2 := seedUser(t, pool, "b2b")
	if id1 == id2 {
		t.Fatalf("seedUser returned the same id twice back-to-back: %d", id1)
	}
}

func ptr(v int64) *int64 { return &v }

// TestWrite_FullEntryPersists writes an entry with every column populated and a
// JSONB metadata object, then reads the row back and asserts each column,
// including the round-tripped metadata JSON.
func TestWrite_FullEntryPersists(t *testing.T) {
	pool := auditTestPool(t)
	actor := seedUser(t, pool, "actor")
	logger := New(pool)

	target := int64(42)
	meta := map[string]any{
		"key":             "abc123",
		"destination_url": "https://example.com",
		"title":           "Example",
		"duplicate":       false,
	}
	if err := logger.Write(context.Background(), Entry{
		ActorID:    &actor,
		UserID:     &actor,
		Action:     ActionLinkCreated,
		TargetType: TargetLink,
		TargetID:   &target,
		Metadata:   meta,
		IP:         "203.0.113.7",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var (
		gotActor  *int64
		gotUser   *int64
		gotAction string
		gotType   *string
		gotTarget *int64
		gotMeta   []byte
		gotIP     *string
	)
	err := pool.QueryRow(context.Background(),
		`SELECT actor_id, user_id, action, target_type, target_id, metadata, host(ip_address)
		   FROM audit_log ORDER BY id DESC LIMIT 1`,
	).Scan(&gotActor, &gotUser, &gotAction, &gotType, &gotTarget, &gotMeta, &gotIP)
	if err != nil {
		t.Fatalf("read back row: %v", err)
	}

	if gotActor == nil || *gotActor != actor {
		t.Errorf("actor_id = %v, want %d", gotActor, actor)
	}
	if gotUser == nil || *gotUser != actor {
		t.Errorf("user_id = %v, want %d", gotUser, actor)
	}
	if gotAction != ActionLinkCreated {
		t.Errorf("action = %q, want %q", gotAction, ActionLinkCreated)
	}
	if gotType == nil || *gotType != TargetLink {
		t.Errorf("target_type = %v, want %q", gotType, TargetLink)
	}
	if gotTarget == nil || *gotTarget != target {
		t.Errorf("target_id = %v, want %d", gotTarget, target)
	}
	if gotIP == nil || *gotIP != "203.0.113.7" {
		t.Errorf("ip_address = %v, want 203.0.113.7", gotIP)
	}

	var roundTripped map[string]any
	if err := json.Unmarshal(gotMeta, &roundTripped); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if roundTripped["key"] != "abc123" || roundTripped["destination_url"] != "https://example.com" {
		t.Errorf("metadata = %v, want key/destination_url preserved", roundTripped)
	}
	if roundTripped["duplicate"] != false {
		t.Errorf("metadata.duplicate = %v, want false", roundTripped["duplicate"])
	}
}

// TestWrite_NilActorAndTargetStoreNull confirms nil pointer fields and nil
// metadata are stored as SQL NULL (e.g. a pre-auth registration_started event).
func TestWrite_NilActorAndTargetStoreNull(t *testing.T) {
	pool := auditTestPool(t)
	logger := New(pool)

	if err := logger.Write(context.Background(), Entry{
		Action: ActionAccountRegistrationStarted,
		// ActorID, UserID, TargetID, Metadata, IP all zero/nil → NULL.
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var (
		actorNull  bool
		userNull   bool
		targetNull bool
		metaNull   bool
		ipNull     bool
		gotAction  string
	)
	err := pool.QueryRow(context.Background(),
		`SELECT actor_id IS NULL, user_id IS NULL, target_id IS NULL,
		        metadata IS NULL, ip_address IS NULL, action
		   FROM audit_log ORDER BY id DESC LIMIT 1`,
	).Scan(&actorNull, &userNull, &targetNull, &metaNull, &ipNull, &gotAction)
	if err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if !actorNull || !userNull || !targetNull || !metaNull || !ipNull {
		t.Errorf("nullable columns: actor=%v user=%v target=%v meta=%v ip=%v, want all NULL",
			!actorNull, !userNull, !targetNull, !metaNull, !ipNull)
	}
	if gotAction != ActionAccountRegistrationStarted {
		t.Errorf("action = %q, want %q", gotAction, ActionAccountRegistrationStarted)
	}
}

// TestWrite_PartialActorWithUser confirms the admin-on-other-user shape (actor
// set, user different) that future admin user-management actions will use:
// both are persisted independently.
func TestWrite_PartialActorWithUser(t *testing.T) {
	pool := auditTestPool(t)
	admin := seedUser(t, pool, "admin")
	victim := seedUser(t, pool, "victim")
	logger := New(pool)

	if err := logger.Write(context.Background(), Entry{
		ActorID:    &admin,
		UserID:     &victim,
		Action:     ActionAccountDeactivated,
		TargetType: TargetUser,
		TargetID:   ptr(victim),
		Metadata:   map[string]any{"reason": "spam", "note": ""},
		IP:         "198.51.100.9",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var gotActor, gotUser int64
	if err := pool.QueryRow(context.Background(),
		`SELECT actor_id, user_id FROM audit_log ORDER BY id DESC LIMIT 1`,
	).Scan(&gotActor, &gotUser); err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if gotActor != admin || gotUser != victim {
		t.Errorf("actor=%d user=%d, want actor=%d user=%d", gotActor, gotUser, admin, victim)
	}
}

// TestWriteTx_RollsBackWithTransaction proves the transactional property every
// auth ceremony (registration.go, login.go, recovery.go, users.go in
// internal/auth) relies on when it calls WriteTx against its own already-open
// tx: the audit row is not an independent write that lands regardless of the
// surrounding transaction's outcome — it lives or dies with tx, exactly like
// every other statement run against it.
//
// This is the general mechanism behind the #0008 acceptance criterion "audit
// rows are written inside the ceremony transaction": every ceremony's audit
// write goes through this exact WriteTx(ctx, tx, entry) call (verified by
// inspection — see the ## Verification note in issues/0008.md), so proving
// the primitive itself rolls back with tx proves every ceremony's audit write
// does too, without needing to force a genuine failure at the specific point
// right after each ceremony's own audit write — which would require adding
// fault-injection hooks to production registration/login/recovery code
// (out of scope for a test-only pass; see the issue's Gotchas for why no
// natural, non-flaky failure point exists after those audit writes). Pair
// this with internal/auth's TestAudit_RegistrationAndLoginSeams, which proves
// the commit side: a successful ceremony's audit rows are actually there.
func TestWriteTx_RollsBackWithTransaction(t *testing.T) {
	pool := auditTestPool(t)
	actor := seedUser(t, pool, "rollback")
	logger := New(pool)
	ctx := context.Background()

	countRows := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM audit_log WHERE actor_id = $1 AND action = $2`,
			actor, ActionAccountLogin,
		).Scan(&n); err != nil {
			t.Fatalf("count audit rows: %v", err)
		}
		return n
	}

	entry := Entry{
		ActorID:    &actor,
		UserID:     &actor,
		Action:     ActionAccountLogin,
		TargetType: TargetUser,
		TargetID:   &actor,
		IP:         "203.0.113.9",
	}

	// Rolled-back transaction: the row must never become visible.
	txRolledBack, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx (rollback case): %v", err)
	}
	if err := logger.WriteTx(ctx, txRolledBack, entry); err != nil {
		t.Fatalf("WriteTx (rollback case): %v", err)
	}
	if err := txRolledBack.Rollback(ctx); err != nil {
		t.Fatalf("rollback tx: %v", err)
	}
	if n := countRows(); n != 0 {
		t.Fatalf("audit rows after rollback = %d, want 0 — WriteTx did not honor the transaction it was given", n)
	}

	// Committed transaction: the same entry through the same call, but
	// committed instead of rolled back — the row must now be visible. This is
	// the paired positive control: it isolates the assertion above to
	// "rollback discards it" rather than "WriteTx never writes anything."
	txCommitted, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx (commit case): %v", err)
	}
	if err := logger.WriteTx(ctx, txCommitted, entry); err != nil {
		t.Fatalf("WriteTx (commit case): %v", err)
	}
	if err := txCommitted.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	if n := countRows(); n != 1 {
		t.Fatalf("audit rows after commit = %d, want 1", n)
	}
}

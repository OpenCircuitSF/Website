package subscribers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testPool returns the package's single shared pool (opened once in
// TestMain, see main_test.go — #0091) or skips if TEST_DATABASE_URL was
// unset.
//
// #0091 round two: this used to TRUNCATE subscriber_interests/subscribers
// on every call (i.e. at the top of every test). Measured clean on an idle
// machine, that truncate was the dominant remaining cost after the pool
// became shared: ~1.4s/test, 71.9s for the package's 50 tests, because a
// full-table TRUNCATE takes a heavy lock regardless of how little data is
// in the table.
//
// The isolation property a per-test truncate buys — "no test sees another
// test's rows" — doesn't actually require the table to be physically empty.
// It only requires that no two tests' rows ever collide under a query one
// of them runs. Every test in this file already seeds its subscribers
// through uniqueEmail(t) (a nanosecond-timestamped local-part), and every
// assertion that reads back rows either filters by the specific id/email a
// test itself created (store.FindByEmail, store.GetByID, a WHERE
// subscriber_id = created.ID query, …) or — where it lists by status or
// interest — treats the result as "contains mine, does not contain the
// other test's known-excluded row" rather than asserting an exact global
// count. See TestIsolation_UniqueDataNeverCollides below for the collision
// case made concrete and deliberately broken.
//
// So the table only needs to be clean once, before the first test — done
// in TestMain — not before every test. testPool no longer truncates; it
// just hands back the shared pool (or skips).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

// seededInterestID resolves a production taxonomy slug (seeded by
// migrations/000009) to its id, for use as a subscriber_interests foreign
// key target. Fails the test if the seed is somehow missing — that would
// mean migration 000009 hasn't been applied to this database.
func seededInterestID(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`SELECT id FROM interests WHERE slug = $1`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("resolve seeded interest %q: %v (has migration 000009 been applied?)", slug, err)
	}
	return id
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-test-%d@example.com", testdb.Unique())
}

// uniqueRawToken returns a manage_token/confirm_token-shaped value unique to
// this call, for the raw-SQL UNIQUE-constraint tests below that need a
// column other than the one they're deliberately duplicating to stay
// collision-free across a `-count=2` repeat. label just keeps two calls in
// the same test visually distinct in a failure message.
func uniqueRawToken(t *testing.T, label string) string {
	t.Helper()
	return fmt.Sprintf("zz-test-raw-token-%s-%d", label, testdb.Unique())
}

func TestCreate_NormalizesEmailAndGeneratesTokens(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	// #0091 round two: this package no longer truncates between tests (see
	// testPool's doc comment), so the literal address this test used to
	// hardcode would collide with its own row on a second `-count=2`
	// iteration. suffix keeps the case/whitespace/+tag shape the test is
	// actually exercising while making the address unique per call.
	suffix := fmt.Sprintf("%d", testdb.Unique())
	raw := "  Zz-Test-Mixed-Case+ABC-" + suffix + "@Example.COM  "
	sub, err := store.Create(context.Background(), NewSignup{
		Email:           raw,
		SignupIP:        "203.0.113.7",
		SignupUserAgent: "test-agent/1.0",
		UTMSource:       "newsletter",
		UTMMedium:       "email",
		UTMCampaign:     "launch",
		ConfirmTTL:      24 * time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantEmail := "zz-test-mixed-case+abc-" + suffix + "@example.com"
	if sub.Email != wantEmail {
		t.Errorf("Email = %q, want %q", sub.Email, wantEmail)
	}
	if sub.Status != StatusPending {
		t.Errorf("Status = %q, want %q", sub.Status, StatusPending)
	}
	if sub.ConfirmToken == nil || *sub.ConfirmToken == "" {
		t.Error("ConfirmToken is nil/empty, want a generated token")
	}
	if sub.ManageToken == "" {
		t.Error("ManageToken is empty, want a generated token")
	}
	if sub.ConfirmToken != nil && sub.ManageToken == *sub.ConfirmToken {
		t.Error("ConfirmToken and ManageToken are equal; want independently random values")
	}
	if sub.SignupIP == nil || *sub.SignupIP != "203.0.113.7" {
		t.Errorf("SignupIP = %v, want 203.0.113.7", sub.SignupIP)
	}
	if sub.SignupUserAgent == nil || *sub.SignupUserAgent != "test-agent/1.0" {
		t.Errorf("SignupUserAgent = %v, want test-agent/1.0", sub.SignupUserAgent)
	}
	if sub.UTMSource == nil || *sub.UTMSource != "newsletter" {
		t.Errorf("UTMSource = %v, want newsletter", sub.UTMSource)
	}
	if sub.ConfirmedAt != nil {
		t.Error("ConfirmedAt should be nil for a freshly created pending subscriber")
	}
}

// TestCreate_PreservesGmailDotsAndPlusTags is the store-layer test the issue
// explicitly requires: lower(trim(...)) normalization must NOT strip Gmail
// dots or +tag suffixes, because they are distinct addresses per RFC and
// people use them deliberately to segment their own mail.
func TestCreate_PreservesGmailDotsAndPlusTags(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	// #0091 round two: this package no longer truncates between tests, so a
	// literal address would collide with its own row on a second
	// `-count=2` iteration. suffix keeps the dot/+tag/whitespace shape each
	// case is actually exercising while making every address unique per
	// call.
	suffix := fmt.Sprintf("%d", testdb.Unique())
	cases := []struct {
		in   string
		want string
	}{
		{"J.O.H.N.zztest-" + suffix + "@gmail.com", "j.o.h.n.zztest-" + suffix + "@gmail.com"}, // dots preserved, only case-folded
		{"jane.zztest+workshops-" + suffix + "@gmail.com", "jane.zztest+workshops-" + suffix + "@gmail.com"},
		{"  Spacey.Zztest-" + suffix + "@Example.com  ", "spacey.zztest-" + suffix + "@example.com"}, // only trim + lowercase
	}
	for _, c := range cases {
		sub, err := store.Create(context.Background(), NewSignup{Email: c.in, ConfirmTTL: time.Hour}, now)
		if err != nil {
			t.Fatalf("Create(%q): %v", c.in, err)
		}
		if sub.Email != c.want {
			t.Errorf("Create(%q).Email = %q, want %q", c.in, sub.Email, c.want)
		}
	}
}

// TestCreate_NonASCIILocalPartNormalizedConsistentlyWithCheckConstraint is
// #0026's review, "narrow the non-ASCII rejection": ǅ (U+01C5) is the
// concrete codepoint the review found Go's strings.ToLower and Postgres's
// lower() disagree on (Go folds it to ǆ U+01C6; Postgres leaves it
// unchanged). Under the old Go-side normalizeEmail, a value already
// "normalized" by Go could still trip subscribers_email_normalized. Now
// that Create computes email as lower(trim($1)) in SQL, the same engine
// that enforces the CHECK also produces the stored value, so this can never
// happen for any codepoint — proved here directly rather than merely
// inferred from removing the Go-side call.
func TestCreate_NonASCIILocalPartNormalizedConsistentlyWithCheckConstraint(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	raw := fmt.Sprintf("ǅ-zztest-%d@example.com", testdb.Unique())
	sub, err := store.Create(context.Background(), NewSignup{Email: raw, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create(%q): %v (want no CHECK-constraint violation)", raw, err)
	}
	// Postgres's lower() leaves ǅ unchanged; assert that's what's stored,
	// not Go's strings.ToLower folding to ǆ.
	if !strings.Contains(sub.Email, "ǅ") {
		t.Errorf("Email = %q, want it to retain ǅ (Postgres lower() leaves this codepoint unchanged)", sub.Email)
	}
}

func TestCreate_DuplicateEmailRejected(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()
	email := uniqueEmail(t)

	if _, err := store.Create(context.Background(), NewSignup{Email: email, ConfirmTTL: time.Hour}, now); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Different case/whitespace, same normalized address.
	_, err := store.Create(context.Background(), NewSignup{Email: " " + email, ConfirmTTL: time.Hour}, now)
	if !errors.Is(err, ErrEmailExists) {
		t.Fatalf("second Create: got err=%v, want ErrEmailExists", err)
	}
}

func TestFindByEmail_NormalizesLookup(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()
	email := uniqueEmail(t)

	created, err := store.Create(context.Background(), NewSignup{Email: email, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.FindByEmail(context.Background(), "  "+email+"  ")
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("FindByEmail returned id %d, want %d", got.ID, created.ID)
	}
}

func TestFindByEmail_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.FindByEmail(context.Background(), "zz-nobody@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestFindByConfirmToken_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.FindByConfirmToken(context.Background(), "not-a-real-token")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestFindByManageToken_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.FindByManageToken(context.Background(), created.ManageToken)
	if err != nil {
		t.Fatalf("FindByManageToken: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("FindByManageToken returned id %d, want %d", got.ID, created.ID)
	}
}

func TestConfirm_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	confirmed, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != StatusActive {
		t.Errorf("Status = %q, want %q", confirmed.Status, StatusActive)
	}
	if confirmed.ConfirmToken != nil {
		t.Errorf("ConfirmToken = %v, want nil (cleared on confirm)", *confirmed.ConfirmToken)
	}
	if confirmed.ConfirmedAt == nil {
		t.Error("ConfirmedAt is nil, want set")
	}

	// The token is single-use: confirming again with the same (now cleared)
	// token must fail rather than silently succeeding a second time.
	_, err = store.Confirm(context.Background(), *created.ConfirmToken, now.Add(2*time.Minute))
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("second Confirm with same token: got err=%v, want ErrTokenInvalid", err)
	}
}

// TestConfirm_EnqueuesWelcomeAtomicallyWithActivation is #0127's load-bearing
// property, mirroring #0126's TestCreate_ClaimsAndEnqueuesConfirmationAtomically:
// a committed Confirm call always has BOTH the subscriber activated AND a
// welcome row queued for it — Confirm's own transaction enqueues welcome
// right alongside recording the confirmed event, so the two either commit
// together or (on any error before Commit) roll back together.
//
// The second half proves "sent exactly once per subscriber": retrying
// Confirm with the same (now single-use, cleared) token — the shape a
// double-click or a page reload produces — returns ErrTokenInvalid and
// leaves the welcome row count at exactly 1, not 2.
func TestConfirm_EnqueuesWelcomeAtomicallyWithActivation(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	confirmed, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != StatusActive {
		t.Fatalf("Status = %q, want %q", confirmed.Status, StatusActive)
	}

	welcomeCount := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'welcome' AND status = 'queued'`,
			confirmed.ID,
		).Scan(&n); err != nil {
			t.Fatalf("counting welcome rows: %v", err)
		}
		return n
	}

	if n := welcomeCount(); n != 1 {
		t.Fatalf("queued welcome rows immediately after Confirm = %d, want 1", n)
	}

	// Retry: same (now cleared) token. Must fail, and must not double-enqueue.
	if _, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(2*time.Minute)); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("retry Confirm with stale token: got err=%v, want ErrTokenInvalid", err)
	}
	if n := welcomeCount(); n != 1 {
		t.Fatalf("queued welcome rows after a retried Confirm = %d, want still 1 (not sent twice)", n)
	}
}

// TestConfirm_SuppressedAddressNeverGetsWelcome proves the #0127 criterion
// "a suppressed address never receives it, even if a confirmation somehow
// lands": a subscriber whose address is suppressed AFTER Create but BEFORE
// the confirm link is clicked still confirms normally (status -> active),
// but Confirm's transaction skips the welcome enqueue entirely.
func TestConfirm_SuppressedAddressNeverGetsWelcome(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := suppressions.Add(context.Background(), NewSuppression{
		Email: created.Email, Reason: SuppressionReasonManual, Note: "test",
	}, now); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	confirmed, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != StatusActive {
		t.Fatalf("Status = %q, want %q (suppression must not block confirming, only the welcome mail)", confirmed.Status, StatusActive)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'welcome'`, confirmed.ID,
	).Scan(&n); err != nil {
		t.Fatalf("counting welcome rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("welcome rows for a suppressed address = %d, want 0", n)
	}
}

// TestConfirm_ComplainedSubscriberNeverGetsWelcome is
// TestConfirm_ComplainedNeverAutoResubscribes's #0127 companion: a
// complained subscriber's Confirm call fails outright (ErrComplainedLocked,
// already proven), so it follows by construction that no welcome is ever
// enqueued for it — this test makes that explicit rather than leaving it
// implicit in "the whole call failed."
func TestConfirm_ComplainedSubscriberNeverGetsWelcome(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET status = $2 WHERE id = $1`, created.ID, StatusComplained,
	); err != nil {
		t.Fatalf("forcing complained status: %v", err)
	}

	if _, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute)); !errors.Is(err, ErrComplainedLocked) {
		t.Fatalf("got err=%v, want ErrComplainedLocked", err)
	}

	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'welcome'`, created.ID,
	).Scan(&n); err != nil {
		t.Fatalf("counting welcome rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("welcome rows for a complained subscriber = %d, want 0", n)
	}
}

// TestConfirm_WelcomePayloadCarriesSelectedInterestNames proves the "which
// interests they selected" content requirement is actually driven by the
// subscriber's real subscriber_interests rows at confirm time, read inside
// Confirm's own transaction (selectedInterestNamesTx) — not a hard-coded or
// empty payload. The welcome row is inspected BEFORE any worker drains it,
// so its payload has not yet been scrubbed by MarkSent.
func TestConfirm_WelcomePayloadCarriesSelectedInterestNames(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	soldering := seededInterestID(t, pool, "soldering")
	robotics := seededInterestID(t, pool, "robotics")
	if err := store.SetInterests(context.Background(), created.ID, []int64{soldering, robotics}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}
	var solderingName, roboticsName string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM interests WHERE id = $1`, soldering).Scan(&solderingName); err != nil {
		t.Fatalf("resolve soldering name: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT name FROM interests WHERE id = $1`, robotics).Scan(&roboticsName); err != nil {
		t.Fatalf("resolve robotics name: %v", err)
	}

	confirmed, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	var payloadText string
	if err := pool.QueryRow(context.Background(),
		`SELECT payload::text FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'welcome'`, confirmed.ID,
	).Scan(&payloadText); err != nil {
		t.Fatalf("select welcome payload: %v", err)
	}
	if !strings.Contains(payloadText, solderingName) || !strings.Contains(payloadText, roboticsName) {
		t.Errorf("welcome payload = %s, want it to contain both selected interest names (%q, %q)", payloadText, solderingName, roboticsName)
	}
	if !strings.Contains(payloadText, created.ManageToken) {
		t.Errorf("welcome payload = %s, want it to contain the subscriber's manage_token", payloadText)
	}
}

// TestConfirm_NoInterestsSelected_WelcomePayloadHasEmptyInterestList proves
// the zero-interest branch: a subscriber who picked nothing still gets a
// welcome row, with an empty (not missing, not erroring) interest_names
// list.
func TestConfirm_NoInterestsSelected_WelcomePayloadHasEmptyInterestList(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	confirmed, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	var payloadText string
	if err := pool.QueryRow(context.Background(),
		`SELECT payload::text FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'welcome'`, confirmed.ID,
	).Scan(&payloadText); err != nil {
		t.Fatalf("select welcome payload: %v", err)
	}
	// jsonb::text round-trips through Postgres's own pretty-printer, which
	// inserts a space after the colon — compare the parsed value, not a
	// hand-rolled substring, so this doesn't depend on that formatting.
	var decoded struct {
		InterestNames []string `json:"interest_names"`
	}
	if err := json.Unmarshal([]byte(payloadText), &decoded); err != nil {
		t.Fatalf("unmarshalling welcome payload %s: %v", payloadText, err)
	}
	if len(decoded.InterestNames) != 0 {
		t.Errorf("welcome payload interest_names = %v, want empty for a subscriber with no interests", decoded.InterestNames)
	}
}

func TestConfirm_ExpiredTokenInvalid(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Minute}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Confirm attempted well after the 1-minute TTL.
	_, err = store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Hour))
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("got err=%v, want ErrTokenInvalid", err)
	}
}

func TestConfirm_UnknownTokenInvalid(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.Confirm(context.Background(), "totally-unknown-token", time.Now())
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("got err=%v, want ErrTokenInvalid", err)
	}
}

// TestConfirm_ComplainedNeverAutoResubscribes is the direct test of
// CLAUDE.md §9's rule: a complained subscriber is never reactivated by
// confirming a (still theoretically live) confirm token. Only an admin
// clears that state. See this issue's Verification for the mutation proof
// against the guard in Store.Confirm.
func TestConfirm_ComplainedNeverAutoResubscribes(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Force the subscriber into complained without clearing its live confirm
	// token, simulating SES reporting a complaint on the confirmation email
	// itself before the recipient ever clicked confirm.
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET status = $2 WHERE id = $1`, created.ID, StatusComplained,
	); err != nil {
		t.Fatalf("forcing complained status: %v", err)
	}

	_, err = store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute))
	if !errors.Is(err, ErrComplainedLocked) {
		t.Fatalf("got err=%v, want ErrComplainedLocked", err)
	}

	got, err := store.FindByEmail(context.Background(), created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if got.Status != StatusComplained {
		t.Fatalf("Status = %q after blocked Confirm, want unchanged %q", got.Status, StatusComplained)
	}
}

func TestUnsubscribe_SetsFieldsAndIsIdempotent(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Unsubscribe(context.Background(), created.ID, SourceOneClick, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if got.Status != StatusUnsubscribed {
		t.Errorf("Status = %q, want %q", got.Status, StatusUnsubscribed)
	}
	if got.UnsubscribedAt == nil {
		t.Error("UnsubscribedAt is nil, want set")
	}
	if got.UnsubscribeSource == nil || *got.UnsubscribeSource != SourceOneClick {
		t.Errorf("UnsubscribeSource = %v, want %q", got.UnsubscribeSource, SourceOneClick)
	}

	// Calling it again (e.g. a double-clicked unsubscribe link) must not error.
	got2, err := store.Unsubscribe(context.Background(), created.ID, SourcePreferences, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("second Unsubscribe: %v", err)
	}
	if got2.Status != StatusUnsubscribed {
		t.Errorf("Status after repeat unsubscribe = %q, want %q", got2.Status, StatusUnsubscribed)
	}
}

func TestUnsubscribe_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.Unsubscribe(context.Background(), 99999999, SourceOneClick, time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

// TestRotateManageToken_Success is #0034's store-level proof that rotation
// actually changes the token (and only the token — status is untouched) on
// an ordinary, non-complained row.
func TestRotateManageToken_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rotated, err := store.RotateManageToken(context.Background(), created.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RotateManageToken: %v", err)
	}
	if rotated.ManageToken == created.ManageToken {
		t.Error("ManageToken unchanged, want a fresh value")
	}
	if rotated.Status != created.Status {
		t.Errorf("Status = %q, want unchanged %q — rotation must not touch status", rotated.Status, created.Status)
	}

	// The OLD token must no longer resolve.
	if _, err := store.FindByManageToken(context.Background(), created.ManageToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByManageToken(old token) err=%v, want ErrNotFound", err)
	}
	// The NEW token must resolve to the same subscriber.
	found, err := store.FindByManageToken(context.Background(), rotated.ManageToken)
	if err != nil {
		t.Fatalf("FindByManageToken(new token): %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("FindByManageToken(new token) resolved id %d, want %d", found.ID, created.ID)
	}
}

// TestRotateManageToken_ComplainedIsNoOp is #0034's carried-in review
// finding: rotating a complained row's token would churn the value on every
// unattended one-click hit against an already-terminal row, invalidating
// every live footer link the person holds for an action that changed
// nothing. RotateManageToken guards statusLockedFromNonAdmin the same way
// every other mutator in this package does.
func TestRotateManageToken_ComplainedIsNoOp(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := store.MarkComplained(context.Background(), created.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	rotated, err := store.RotateManageToken(context.Background(), created.ID, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("RotateManageToken: %v", err)
	}
	if rotated.ManageToken != created.ManageToken {
		t.Error("ManageToken changed on a complained row, want unchanged (no-op)")
	}
	if rotated.Status != StatusComplained {
		t.Errorf("Status = %q, want %q", rotated.Status, StatusComplained)
	}
}

// TestSetInterests_ZeroInterestsIsValid directly exercises PRD §6.1's rule
// that a subscriber with zero interests is valid and expected (general
// announcements only), not an error condition.
func TestSetInterests_ZeroInterestsIsValid(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.SetInterests(context.Background(), created.ID, nil); err != nil {
		t.Fatalf("SetInterests(nil): %v", err)
	}
	ids, err := store.InterestIDs(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("InterestIDs = %v, want empty", ids)
	}

	// The subscriber row itself must still be perfectly usable.
	got, err := store.FindByEmail(context.Background(), created.Email)
	if err != nil {
		t.Fatalf("FindByEmail after zero-interest SetInterests: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("unexpected subscriber returned: %+v", got)
	}
}

func TestSetInterests_ReplacesFully(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	soldering := seededInterestID(t, pool, "soldering")
	robotics := seededInterestID(t, pool, "robotics")
	beginner := seededInterestID(t, pool, "beginner")

	if err := store.SetInterests(context.Background(), created.ID, []int64{soldering, robotics}); err != nil {
		t.Fatalf("SetInterests (first): %v", err)
	}
	ids, err := store.InterestIDs(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("InterestIDs after first SetInterests = %v, want 2 entries", ids)
	}

	// Replacing with a different set must fully supersede the first, not merge.
	if err := store.SetInterests(context.Background(), created.ID, []int64{beginner}); err != nil {
		t.Fatalf("SetInterests (second): %v", err)
	}
	ids, err = store.InterestIDs(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("InterestIDs after second SetInterests: %v", err)
	}
	if len(ids) != 1 || ids[0] != beginner {
		t.Fatalf("InterestIDs = %v, want exactly [%d]", ids, beginner)
	}
}

// TestUnsubscribe_ComplainedNeverAutoResubscribes is the direct guard test
// for Unsubscribe, analogous to TestConfirm_ComplainedNeverAutoResubscribes.
// Before the #0025 fix, Unsubscribe wrote status unconditionally and this
// overwrote a recorded complaint with "unsubscribed" — see this issue's
// Review notes for the reproduced laundering chain.
func TestUnsubscribe_ComplainedNeverAutoResubscribes(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.MarkComplained(context.Background(), created.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	// PRD §6.5 requires the one-click unsubscribe endpoint to answer a
	// neutral 200 for every token state, so Unsubscribe on a complained
	// subscriber must succeed (no error) while leaving status untouched.
	got, err := store.Unsubscribe(context.Background(), created.ID, SourceOneClick, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Unsubscribe on complained subscriber: got err=%v, want nil (must be a silent no-op)", err)
	}
	if got.Status != StatusComplained {
		t.Fatalf("Status after Unsubscribe on complained subscriber = %q, want unchanged %q", got.Status, StatusComplained)
	}
	if got.UnsubscribedAt != nil {
		t.Errorf("UnsubscribedAt = %v, want nil (unsubscribe must not be recorded over a complaint)", got.UnsubscribedAt)
	}

	// Confirm the row genuinely wasn't touched, not just the returned value.
	reread, err := store.FindByEmail(context.Background(), created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if reread.Status != StatusComplained {
		t.Fatalf("Status on reread = %q, want %q", reread.Status, StatusComplained)
	}
}

// TestMarkBounced_ComplainedStaysComplained is the direct guard test for
// MarkBounced/setStatus: a bounce notification arriving for an
// already-complained address must not erase the complaint.
func TestMarkBounced_ComplainedStaysComplained(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.MarkComplained(context.Background(), created.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	got, err := store.MarkBounced(context.Background(), created.ID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("MarkBounced on complained subscriber: got err=%v, want nil (must be a silent no-op)", err)
	}
	if got.Status != StatusComplained {
		t.Fatalf("Status after MarkBounced on complained subscriber = %q, want unchanged %q", got.Status, StatusComplained)
	}
}

// TestComplainedLaundering_ChainBroken is the end-to-end regression test for
// the #0025 review bounce: MarkComplained -> Unsubscribe -> Confirm must
// never reach active. Before the fix this chain reached "active" via
// "unsubscribed" in between (reproduced and recorded in this issue's Review
// notes and Verification).
func TestComplainedLaundering_ChainBroken(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()
	ctx := context.Background()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.MarkComplained(ctx, created.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	unsub, err := store.Unsubscribe(ctx, created.ID, SourceOneClick, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if unsub.Status != StatusComplained {
		t.Fatalf("status after MarkComplained then Unsubscribe = %q, want %q (complaint must not be launderable)", unsub.Status, StatusComplained)
	}

	// Simulate a fresh confirm token being minted for this address (what
	// #0026's subscribe handler would do for what it believes is a plain
	// unsubscribed->new-signup case) and attempt to confirm it.
	freshToken, err := newToken()
	if err != nil {
		t.Fatalf("newToken: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE subscribers SET confirm_token = $2, confirm_expires_at = $3 WHERE id = $1`,
		created.ID, freshToken, now.Add(7*24*time.Hour),
	); err != nil {
		t.Fatalf("minting fresh confirm token: %v", err)
	}

	_, err = store.Confirm(ctx, freshToken, now.Add(3*time.Minute))
	if !errors.Is(err, ErrComplainedLocked) {
		t.Fatalf("Confirm after MarkComplained->Unsubscribe: got err=%v, want ErrComplainedLocked", err)
	}

	final, err := store.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if final.Status != StatusComplained {
		t.Fatalf("final status = %q, want %q (chain must never reach active)", final.Status, StatusComplained)
	}
}

// TestAdminClearComplaint_Success is the deliberate, tested admin-only path
// out of complained that CLAUDE.md §9 requires to exist somewhere.
func TestAdminClearComplaint_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()
	ctx := context.Background()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.MarkComplained(ctx, created.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	cleared, err := store.AdminClearComplaint(ctx, created.ID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("AdminClearComplaint: %v", err)
	}
	if cleared.Status != StatusUnsubscribed {
		t.Errorf("Status after AdminClearComplaint = %q, want %q", cleared.Status, StatusUnsubscribed)
	}
	if cleared.UnsubscribeSource == nil || *cleared.UnsubscribeSource != SourceAdmin {
		t.Errorf("UnsubscribeSource = %v, want %q", cleared.UnsubscribeSource, SourceAdmin)
	}

	// Now that status is no longer complained, the ordinary guarded methods
	// must be able to act on the row normally again (proves the admin path
	// is a real, functioning exit from the lock, not just a status flip that
	// leaves the row otherwise stuck).
	unsub, err := store.Unsubscribe(ctx, created.ID, SourceOneClick, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Unsubscribe after AdminClearComplaint: %v", err)
	}
	if unsub.UnsubscribeSource == nil || *unsub.UnsubscribeSource != SourceOneClick {
		t.Errorf("UnsubscribeSource after post-clear Unsubscribe = %v, want %q (must no longer be locked)", unsub.UnsubscribeSource, SourceOneClick)
	}
}

func TestAdminClearComplaint_NotComplained(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()
	ctx := context.Background()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = store.AdminClearComplaint(ctx, created.ID, now)
	if !errors.Is(err, ErrNotComplained) {
		t.Fatalf("got err=%v, want ErrNotComplained", err)
	}
}

func TestAdminClearComplaint_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.AdminClearComplaint(context.Background(), 99999999, time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestMarkBouncedAndMarkComplained(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bounced, err := store.MarkBounced(context.Background(), created.ID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("MarkBounced: %v", err)
	}
	if bounced.Status != StatusBounced {
		t.Errorf("Status = %q, want %q", bounced.Status, StatusBounced)
	}

	complained, err := store.MarkComplained(context.Background(), created.ID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}
	if complained.Status != StatusComplained {
		t.Errorf("Status = %q, want %q", complained.Status, StatusComplained)
	}
}

// --- Database-constraint tests (raw SQL, bypassing the store's own
// validation) so the migration's CHECK/UNIQUE/FK constraints are what's
// actually under test. See this issue's Verification for the mutation
// proofs against these. ---

func TestDB_EmailNormalizedConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token)
		 VALUES ('  Not-Normalized@Example.com', 'zz-test-raw-token-1')`)
	if err == nil {
		t.Fatal("raw INSERT with non-normalized email succeeded; want subscribers_email_normalized CHECK violation")
	}
}

func TestDB_StatusCheckConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, status, manage_token)
		 VALUES ('zz-test-status@example.com', 'not-a-real-status', 'zz-test-raw-token-2')`)
	if err == nil {
		t.Fatal("raw INSERT with invalid status succeeded; want subscribers_status_check CHECK violation")
	}
}

// #0091 round two: every field these three raw-INSERT tests don't
// deliberately duplicate (the whole point of a UNIQUE-constraint test is
// duplicating exactly one column) must itself be unique per call. This
// package no longer truncates between tests, so a literal like
// 'zz-test-raw-token-3' or 'zz-test-a@example.com' — fine when every test
// started from an empty table — collides with its own prior row on a
// second `-count=2` iteration and fails on the FIRST insert, before the
// test ever reaches the duplicate-column assertion it's actually about.
// uniqueEmail(t) and uniqueRawToken(t) below exist for exactly this.

func TestDB_EmailUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if _, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2)`, email, uniqueRawToken(t, "3"),
	); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2)`, email, uniqueRawToken(t, "4"))
	if err == nil {
		t.Fatal("second raw insert with duplicate email succeeded; want UNIQUE violation")
	}
}

func TestDB_ManageTokenUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	token := fmt.Sprintf("zz-test-shared-token-%d", testdb.Unique())

	if _, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2)`, uniqueEmail(t), token,
	); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2)`, uniqueEmail(t), token)
	if err == nil {
		t.Fatal("second raw insert with duplicate manage_token succeeded; want UNIQUE violation")
	}
}

func TestDB_ConfirmTokenUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	token := fmt.Sprintf("zz-test-shared-confirm-%d", testdb.Unique())

	if _, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token, confirm_token)
		 VALUES ($1, $2, $3)`, uniqueEmail(t), uniqueRawToken(t, "5"), token,
	); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token, confirm_token)
		 VALUES ($1, $2, $3)`, uniqueEmail(t), uniqueRawToken(t, "6"), token)
	if err == nil {
		t.Fatal("second raw insert with duplicate confirm_token succeeded; want UNIQUE violation")
	}
}

// TestDB_SubscriberInterestsCascadeDelete proves ON DELETE CASCADE on both
// FKs of subscriber_interests: deleting a subscriber (or an interest) must
// remove its join rows automatically, never leaving an orphan.
func TestDB_SubscriberInterestsCascadeDelete(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	interestID := seededInterestID(t, pool, "homelab")
	if err := store.SetInterests(ctx, created.ID, []int64{interestID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	var before int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM subscriber_interests WHERE subscriber_id = $1`, created.ID,
	).Scan(&before); err != nil {
		t.Fatalf("counting before delete: %v", err)
	}
	if before != 1 {
		t.Fatalf("expected 1 subscriber_interests row before delete, got %d", before)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("deleting subscriber: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM subscriber_interests WHERE subscriber_id = $1`, created.ID,
	).Scan(&after); err != nil {
		t.Fatalf("counting after delete: %v", err)
	}
	if after != 0 {
		t.Fatalf("subscriber_interests row survived subscriber delete; ON DELETE CASCADE not in effect (got %d rows)", after)
	}
}

// TestCreate_ClaimsAndEnqueuesConfirmationAtomically replaces this
// package's pre-#0126 test asserting that Create left confirm_sent_at nil
// (the #0026-era design, where a separate later claim stamped it only
// after a successful send). #0126 changed that: Create now claims and
// enqueues the confirmation INSIDE its own transaction (a new row has no
// cooldown to lose, so the claim there is unconditional), so
// confirm_sent_at IS stamped by the time Create returns, and a matching
// outbound_queue row already exists — see this issue's plan §4 for why a
// two-step Create-then-claim would leave exactly the window "a committed
// signup can never have an unsent confirmation" forbids.
func TestCreate_ClaimsAndEnqueuesConfirmationAtomically(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.ConfirmSentAt == nil || !sub.ConfirmSentAt.Equal(now) {
		t.Fatalf("ConfirmSentAt = %v, want stamped to %v (Create's own atomic claim)", sub.ConfirmSentAt, now)
	}

	var queuedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation' AND status = 'queued'`,
		sub.ID,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("queued confirmation rows for subscriber %d = %d, want 1", sub.ID, queuedCount)
	}

	var eventCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'signup_requested'`,
		sub.ID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("counting subscriber_events rows: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("signup_requested events for subscriber %d = %d, want 1", sub.ID, eventCount)
	}
}

// errPostEnqueueCrash is the sentinel postEnqueueCommitHook returns in the
// two tests below — never wrapped by Create/ClaimAndEnqueueConfirmation, so
// errors.Is proves the failure actually came from the hook and not some
// other path.
var errPostEnqueueCrash = errors.New("subscribers_test: simulated crash after enqueue, before commit")

// TestCreate_FailureAfterEnqueue_CommitsNeither is #0126's phase-3 review's
// defect 3 regression test — the load-bearing property's own negative half.
// Plan §10 named a positive test for this property plus, in its own words,
// "its negative: a failure injected between them commits neither" — but
// only the positive half (TestCreate_ClaimsAndEnqueuesConfirmationAtomically,
// above) was ever written. This is that missing negative half.
//
// It injects a failure via postEnqueueCommitHook at the exact point a real
// crash between the subscribers INSERT and the outbound_queue EnqueueTx
// would land — after EnqueueTx has run, before tx.Commit — and asserts that
// NEITHER row exists afterward: the subscribers row must not have been
// created, and no outbound_queue row must exist for it.
//
// Mutation-proved (see issues/0126.md's Fix pass): temporarily moving
// Create's EnqueueTx call to run AFTER tx.Commit — the exact regression
// this issue forbids, and the one the phase-3 reviewer demonstrated left
// the pre-existing suite fully green — makes this test fail, because the
// subscribers row survives the (now too-late) hook's error while the
// outbound_queue row never gets created at all: two committed states
// diverging in exactly the way the criterion forbids.
func TestCreate_FailureAfterEnqueue_CommitsNeither(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	postEnqueueCommitHook = func() error { return errPostEnqueueCrash }
	t.Cleanup(func() { postEnqueueCommitHook = nil })

	email := uniqueEmail(t)
	_, err := store.Create(context.Background(), NewSignup{Email: email, ConfirmTTL: time.Hour}, time.Now())
	if !errors.Is(err, errPostEnqueueCrash) {
		t.Fatalf("Create error = %v, want errPostEnqueueCrash", err)
	}

	var subCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscribers WHERE email = $1`, email,
	).Scan(&subCount); err != nil {
		t.Fatalf("counting subscribers: %v", err)
	}
	if subCount != 0 {
		t.Fatalf("subscribers rows for %q after a failure between enqueue and commit = %d, want 0 (Create's insert must have rolled back too)", email, subCount)
	}

	var queueCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1`, email,
	).Scan(&queueCount); err != nil {
		t.Fatalf("counting outbound_queue: %v", err)
	}
	if queueCount != 0 {
		t.Fatalf("outbound_queue rows for %q after a failure between enqueue and commit = %d, want 0", email, queueCount)
	}
}

// TestClaimAndEnqueueConfirmation_FailureAfterEnqueue_CommitsNeither is
// TestCreate_FailureAfterEnqueue_CommitsNeither's counterpart for
// ClaimAndEnqueueConfirmation, per the plan §10's "Same for
// ClaimAndEnqueueConfirmation." A failure injected after its EnqueueTx call
// must leave confirm_sent_at un-stamped (still NULL, the claim rolled back)
// AND no outbound_queue row — not one without the other.
func TestClaimAndEnqueueConfirmation_FailureAfterEnqueue_CommitsNeither(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriber(t, pool, now)

	postEnqueueCommitHook = func() error { return errPostEnqueueCrash }
	t.Cleanup(func() { postEnqueueCommitHook = nil })

	claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, now, time.Hour, time.Hour, RestartSignupInput{})
	if !errors.Is(err, errPostEnqueueCrash) {
		t.Fatalf("ClaimAndEnqueueConfirmation error = %v, want errPostEnqueueCrash", err)
	}
	if claimed {
		t.Fatal("claimed = true despite the injected post-enqueue failure, want false")
	}

	_, confirmSentAt := readSubscriberByID(t, pool, sub.ID)
	if confirmSentAt != nil {
		t.Fatalf("confirm_sent_at = %v after a failure between enqueue and commit, want nil (the claim must have rolled back too)", *confirmSentAt)
	}

	var queueCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1`, sub.ID,
	).Scan(&queueCount); err != nil {
		t.Fatalf("counting outbound_queue: %v", err)
	}
	if queueCount != 0 {
		t.Fatalf("outbound_queue rows for subscriber %d after a failure between enqueue and commit = %d, want 0", sub.ID, queueCount)
	}
}

// TestCreate_SyntheticSkipsClaimAndEnqueue proves #0046's dedicated
// campaign-test recipient row (admin_campaign_preview.go's
// ensureTestRecipient, NewSignup.Synthetic: true) still never triggers a
// real confirmation send — the behavior it had before #0126, preserved
// deliberately rather than incidentally (see Create's doc comment).
func TestCreate_SyntheticSkipsClaimAndEnqueue(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour, Synthetic: true}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.ConfirmSentAt != nil {
		t.Fatalf("ConfirmSentAt = %v for a synthetic signup, want nil (no real send should ever be claimed)", *sub.ConfirmSentAt)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1`, sub.ID,
	).Scan(&count); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("outbound_queue rows for a synthetic subscriber = %d, want 0", count)
	}
}

// coldConfirmationSubscriber inserts a pending subscriber row DIRECTLY
// (bypassing Create, which since #0126 claims and enqueues a confirmation
// of its own) with confirm_sent_at NULL and no outbound_queue row, so
// ClaimAndEnqueueConfirmation's own claim-and-enqueue behavior can be
// tested in isolation, with a known starting queue-row count of zero.
func coldConfirmationSubscriber(t *testing.T, pool *pgxpool.Pool, now time.Time) Subscriber {
	t.Helper()
	return coldConfirmationSubscriberWithStatus(t, pool, now, StatusPending)
}

// coldConfirmationSubscriberWithStatus is coldConfirmationSubscriber
// parameterized on status — #0341's TestClaimAndEnqueueConfirmation_
// RefusesComplainedRow uses status=StatusComplained to prove the WHERE
// clause's `AND status = 'pending'` guard, added by that issue, actually
// refuses a claim it would otherwise win. A real complained row would not
// carry a live confirm_token in practice (Confirm/setStatusTx never leave
// one), but the point of this helper is isolating the status guard alone:
// giving the row a token and a NULL confirm_sent_at means the ONLY thing
// that can make the claim lose is the status check, not a missing token or
// an active cooldown.
func coldConfirmationSubscriberWithStatus(t *testing.T, pool *pgxpool.Pool, now time.Time, status string) Subscriber {
	t.Helper()
	token := fmt.Sprintf("ctok-%d", testdb.Unique())
	manageToken := fmt.Sprintf("mtok-%d", testdb.Unique())
	row := pool.QueryRow(context.Background(),
		`INSERT INTO subscribers
		     (email, status, confirm_token, confirm_sent_at, confirm_expires_at, manage_token, created_at, updated_at)
		 VALUES (lower(trim($1)), $2, $3, NULL, $4, $5, $6, $6)
		 RETURNING `+subscriberColumns,
		uniqueEmail(t), status, token, now.Add(time.Hour), manageToken, now,
	)
	// Scans via scanSubscriber (not a hand-written destination list) so this
	// helper can never drift out of sync with subscriberColumns the way a
	// duplicated Scan(...) call did when #0125 widened that column list —
	// see store.go's scanSubscriber.
	sub, err := scanSubscriber(row)
	if err != nil {
		t.Fatalf("seed cold-confirmation subscriber (status=%s): %v", status, err)
	}
	return sub
}

// TestClaimAndEnqueueConfirmation_ClaimsWhenColdAndEnqueues is the
// store-layer proof that a successful claim both stamps confirm_sent_at and
// commits a matching outbound_queue row in the same transaction.
func TestClaimAndEnqueueConfirmation_ClaimsWhenColdAndEnqueues(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriber(t, pool, now)

	sentAt := now.Add(time.Minute)
	claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, sentAt, time.Hour, time.Hour, RestartSignupInput{})
	if err != nil {
		t.Fatalf("ClaimAndEnqueueConfirmation: %v", err)
	}
	if !claimed {
		t.Fatal("claimed = false on a fresh (confirm_sent_at = NULL) row, want true")
	}

	status, confirmSentAt := readSubscriberByID(t, pool, sub.ID)
	if confirmSentAt == nil || !confirmSentAt.Equal(sentAt) {
		t.Fatalf("confirm_sent_at = %v, want %v", confirmSentAt, sentAt)
	}
	if status != StatusPending {
		t.Errorf("Status = %q, want unchanged %q", status, StatusPending)
	}

	var queuedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation' AND status = 'queued'`,
		sub.ID,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("queued confirmation rows = %d, want 1", queuedCount)
	}
}

// TestClaimAndEnqueueConfirmation_ReusesLiveToken is #0314's "do not mint
// unconditionally" half: a person who submits the form twice inside the
// TTL, and clicks the OLDER of the two resulting emails, must still land on
// a working link. Both claims here reuse the SAME still-live token — proven
// by decoding both enqueued payloads AND by confirming with the very first
// token afterward, an oracle that is not a copy of the mint-vs-reuse `if`
// under test.
//
// Mutation M1b (#0314's plan): mint unconditionally rather than only when
// stale. Must fail — the second payload's token would differ from the
// first, and the first token would no longer resolve via Confirm once the
// live-but-unused first token is overwritten by the second claim's mint.
func TestClaimAndEnqueueConfirmation_ReusesLiveToken(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriber(t, pool, now) // confirm_expires_at = now+1h: live throughout this test
	firstToken := *sub.ConfirmToken

	// cooldown is deliberately tiny (not zero — Postgres's `<` comparison
	// on an equal timestamp would still block a same-instant second claim)
	// so the SECOND claim below is not itself refused by the cooldown this
	// test is not about.
	const cooldown = time.Nanosecond
	if claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, now, cooldown, time.Hour, RestartSignupInput{}); err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}

	second := now.Add(time.Minute)
	if claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, second, cooldown, time.Hour, RestartSignupInput{}); err != nil || !claimed {
		t.Fatalf("second claim: claimed=%v err=%v", claimed, err)
	}

	rows, err := pool.Query(context.Background(),
		`SELECT payload::text FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation' ORDER BY id`, sub.ID)
	if err != nil {
		t.Fatalf("querying enqueued payloads: %v", err)
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var payloadText string
		if err := rows.Scan(&payloadText); err != nil {
			t.Fatalf("scanning payload: %v", err)
		}
		var decoded struct {
			ConfirmToken string `json:"confirm_token"`
		}
		if err := json.Unmarshal([]byte(payloadText), &decoded); err != nil {
			t.Fatalf("unmarshalling payload %s: %v", payloadText, err)
		}
		tokens = append(tokens, decoded.ConfirmToken)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating rows: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("enqueued confirmation rows = %d, want 2", len(tokens))
	}
	for i, tok := range tokens {
		if tok != firstToken {
			t.Errorf("payload[%d].confirm_token = %q, want the ORIGINAL live token %q reused, not a fresh one", i, tok, firstToken)
		}
	}

	confirmed, err := store.Confirm(context.Background(), firstToken, second.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm with the first message's token: %v", err)
	}
	if confirmed.Status != StatusActive {
		t.Errorf("Status after confirming with the first (older) message's token = %q, want %q", confirmed.Status, StatusActive)
	}
}

// TestClaimAndEnqueueConfirmation_RefusesComplainedRow is #0341's proof
// that ClaimAndEnqueueConfirmation now guards `status = 'pending'` the same
// way its twin AdminResendConfirmation does (ErrNotPending). Before #0341's
// fix this method's WHERE clause checked only the confirm_sent_at cooldown,
// so a row that had transitioned to complained between a caller's read and
// this claim (e.g. an SES complaint landing mid-request) would still be
// claimed and mailed a confirmation — this test fails on that code (see
// this test's own oracle below) and passes once the WHERE clause carries
// `AND status = $4` with StatusPending.
//
// This does not by itself prove a complained address can be resubscribed —
// Confirm already refuses a complained row outright (ErrComplainedLocked,
// store.go) regardless of whether a link was ever mailed to it. What this
// test proves is the narrower, still load-bearing property #0341 was filed
// over: the claim itself should refuse a complained row rather than
// silently mailing one, so the two claim paths enforce the same rule
// instead of one being weaker than the other.
func TestClaimAndEnqueueConfirmation_RefusesComplainedRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriberWithStatus(t, pool, now, StatusComplained)

	claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, now, time.Hour, time.Hour, RestartSignupInput{})
	if err != nil {
		t.Fatalf("ClaimAndEnqueueConfirmation: %v", err)
	}
	if claimed {
		t.Fatal("claimed = true for a status=complained row, want false (the status guard must refuse this claim)")
	}

	status, confirmSentAt := readSubscriberByID(t, pool, sub.ID)
	if status != StatusComplained {
		t.Errorf("Status = %q after a refused claim, want unchanged %q", status, StatusComplained)
	}
	if confirmSentAt != nil {
		t.Fatalf("confirm_sent_at = %v after a refused claim, want nil (the claim must not have stamped it)", *confirmSentAt)
	}

	var queuedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1`, sub.ID,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 0 {
		t.Fatalf("outbound_queue rows for a refused complained-row claim = %d, want 0 (no confirmation should have been enqueued)", queuedCount)
	}
}

func TestClaimAndEnqueueConfirmation_RefusesWithinCooldown_EnqueuesNothing(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriber(t, pool, now)

	if claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, now, time.Hour, time.Hour, RestartSignupInput{}); err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v, want true, nil", claimed, err)
	}

	// A second claim attempt 30 minutes later — still within the hour
	// cooldown — must be refused, leave confirm_sent_at unchanged, and
	// enqueue nothing.
	second := now.Add(30 * time.Minute)
	claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, second, time.Hour, time.Hour, RestartSignupInput{})
	if err != nil {
		t.Fatalf("second ClaimAndEnqueueConfirmation: %v", err)
	}
	if claimed {
		t.Fatal("claimed = true within the cooldown window, want false")
	}
	_, confirmSentAt := readSubscriberByID(t, pool, sub.ID)
	if confirmSentAt == nil || !confirmSentAt.Equal(now) {
		t.Errorf("confirm_sent_at = %v, want unchanged %v", confirmSentAt, now)
	}

	var queuedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation'`, sub.ID,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("queued confirmation rows after a refused second claim = %d, want 1 (only the first claim's row)", queuedCount)
	}
}

func TestClaimAndEnqueueConfirmation_SucceedsAgainAfterCooldownExpires(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriber(t, pool, now)

	if claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, now, time.Hour, time.Hour, RestartSignupInput{}); err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v, want true, nil", claimed, err)
	}

	later := now.Add(90 * time.Minute)
	claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, later, time.Hour, time.Hour, RestartSignupInput{})
	if err != nil {
		t.Fatalf("second ClaimAndEnqueueConfirmation: %v", err)
	}
	if !claimed {
		t.Fatal("claimed = false after the cooldown expired, want true")
	}
	_, confirmSentAt := readSubscriberByID(t, pool, sub.ID)
	if confirmSentAt == nil || !confirmSentAt.Equal(later) {
		t.Errorf("confirm_sent_at = %v, want re-stamped to %v", confirmSentAt, later)
	}

	var queuedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation'`, sub.ID,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 2 {
		t.Fatalf("queued confirmation rows after two successful claims = %d, want 2", queuedCount)
	}
}

// TestClaimAndEnqueueConfirmation_OnlyOneWinnerUnderConcurrency is the
// store-layer proof of #0026's review finding 3, carried forward under
// #0126's atomic claim-and-enqueue: N concurrent claim attempts against the
// SAME cold row must produce exactly one winner and exactly one enqueued
// row, never more.
func TestClaimAndEnqueueConfirmation_OnlyOneWinnerUnderConcurrency(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	sub := coldConfirmationSubscriber(t, pool, now)

	const concurrency = 8
	results := make(chan bool, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := store.ClaimAndEnqueueConfirmation(context.Background(), sub, now, time.Hour, time.Hour, RestartSignupInput{})
			if err != nil {
				t.Errorf("ClaimAndEnqueueConfirmation: %v", err)
				return
			}
			results <- claimed
		}()
	}
	wg.Wait()
	close(results)

	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d out of %d concurrent claims against one cold row, want exactly 1", winners, concurrency)
	}

	var queuedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation'`, sub.ID,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("queued confirmation rows after %d concurrent claims = %d, want exactly 1", concurrency, queuedCount)
	}
}

func TestClaimAndEnqueueAlreadySubscribed_ClaimsOnceThenCoolsDown(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	id := seedActiveSubscriber(t, pool)
	sub, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	claimed, err := store.ClaimAndEnqueueAlreadySubscribed(ctx, sub, now, time.Hour)
	if err != nil {
		t.Fatalf("first ClaimAndEnqueueAlreadySubscribed: %v", err)
	}
	if !claimed {
		t.Fatal("claimed = false on a fresh (already_subscribed_sent_at = NULL) row, want true")
	}

	// A second submit a minute later must be refused.
	claimed2, err := store.ClaimAndEnqueueAlreadySubscribed(ctx, sub, now.Add(time.Minute), time.Hour)
	if err != nil {
		t.Fatalf("second ClaimAndEnqueueAlreadySubscribed: %v", err)
	}
	if claimed2 {
		t.Fatal("claimed = true on the second submit within the cooldown, want false")
	}

	var queuedCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'already_subscribed'`, id,
	).Scan(&queuedCount); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if queuedCount != 1 {
		t.Fatalf("queued already_subscribed rows = %d, want 1 (only the first, successful claim)", queuedCount)
	}
}

// readSubscriberByID is a small test-only raw-SQL reader so the Claim*
// tests above can check status/confirm_sent_at without a public
// single-column getter existing just for tests.
func readSubscriberByID(t *testing.T, pool *pgxpool.Pool, id int64) (status string, confirmSentAt *time.Time) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT status, confirm_sent_at FROM subscribers WHERE id = $1`, id,
	).Scan(&status, &confirmSentAt)
	if err != nil {
		t.Fatalf("read subscriber %d: %v", id, err)
	}
	return status, confirmSentAt
}

// seedActiveSubscriber inserts a minimal active subscriber row directly
// (bypassing Create, which only ever produces pending rows) for tests that
// need an active row to claim an already-subscribed send against.
func seedActiveSubscriber(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO subscribers (email, status, manage_token)
		 VALUES ($1, $2, $3) RETURNING id`,
		uniqueEmail(t), StatusActive, fmt.Sprintf("mtok-%d", testdb.Unique()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed active subscriber: %v", err)
	}
	return id
}

// TestRestartSignup_UnsubscribedGetsFreshTokenAndPending is PRD §6.3's
// "unsubscribed → treat as new signup; fresh confirm token" branch, proved
// at the store layer: a fresh (different) confirm token, status back to
// pending, confirmed_at/confirm_sent_at cleared, and the consent evidence
// (signup_ip/user_agent/utm_*) refreshed to the new signup event rather
// than left pointing at the original, possibly long-stale, signup.
func TestRestartSignup_UnsubscribedGetsFreshTokenAndPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := store.Create(ctx, NewSignup{
		Email:      uniqueEmail(t),
		SignupIP:   "203.0.113.1",
		ConfirmTTL: time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	oldConfirmToken := *created.ConfirmToken

	if _, err := store.Unsubscribe(ctx, created.ID, SourceOneClick, now.Add(time.Minute)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	restarted, err := store.RestartSignup(ctx, created.ID, RestartSignupInput{
		SignupIP:        "198.51.100.9",
		SignupUserAgent: "restart-agent/2.0",
		UTMSource:       "restart-source",
		ConfirmTTL:      2 * time.Hour,
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}

	if restarted.Status != StatusPending {
		t.Errorf("Status = %q, want %q", restarted.Status, StatusPending)
	}
	if restarted.ConfirmToken == nil || *restarted.ConfirmToken == "" {
		t.Fatal("ConfirmToken is nil/empty after RestartSignup")
	}
	if *restarted.ConfirmToken == oldConfirmToken {
		t.Error("ConfirmToken unchanged by RestartSignup, want a fresh value")
	}
	if restarted.ManageToken != created.ManageToken {
		t.Error("ManageToken changed by RestartSignup, want unchanged (long-lived)")
	}
	if restarted.ConfirmedAt != nil {
		t.Error("ConfirmedAt should be nil after RestartSignup")
	}
	if restarted.ConfirmSentAt != nil {
		t.Error("ConfirmSentAt should be nil after RestartSignup, matching Create's decision")
	}
	if restarted.ConfirmExpiresAt == nil || !restarted.ConfirmExpiresAt.After(now.Add(2*time.Minute)) {
		t.Errorf("ConfirmExpiresAt = %v, want in the future", restarted.ConfirmExpiresAt)
	}
	if restarted.SignupIP == nil || *restarted.SignupIP != "198.51.100.9" {
		t.Errorf("SignupIP = %v, want refreshed to 198.51.100.9", restarted.SignupIP)
	}
	if restarted.UTMSource == nil || *restarted.UTMSource != "restart-source" {
		t.Errorf("UTMSource = %v, want refreshed to restart-source", restarted.UTMSource)
	}
}

// TestRestartSignup_ClearsUnsubscribedAtAndSource is #0324 item 2's direct
// proof at the row level: RestartSignup must clear unsubscribed_at and
// unsubscribe_source, the same treatment it already gives confirmed_at and
// confirm_sent_at, so a restarted row carries no stale departure evidence
// forward — see this method's own #0324 doc comment for why leaving them set
// left a persistent, unmatched -1 in Growth30Days' unsubscribed_30d.
func TestRestartSignup_ClearsUnsubscribedAtAndSource(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Unsubscribe(ctx, created.ID, SourceOneClick, now.Add(time.Minute)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	restarted, err := store.RestartSignup(ctx, created.ID, RestartSignupInput{ConfirmTTL: time.Hour}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	if restarted.UnsubscribedAt != nil {
		t.Errorf("UnsubscribedAt = %v after RestartSignup, want nil", restarted.UnsubscribedAt)
	}
	if restarted.UnsubscribeSource != nil {
		t.Errorf("UnsubscribeSource = %v after RestartSignup, want nil", *restarted.UnsubscribeSource)
	}
}

// TestGrowth30Days_ConfirmUnsubscribeRestartNetsZero is #0324 item 2's
// exact three-step measurement: confirm, unsubscribe, restart, all inside
// the same 30-day window, on an ordinary website signup (no import
// involved). Before this fix: after Confirm net_30d was +1, after
// Unsubscribe it was back to 0 (confirmed_30d's +1 offset by
// unsubscribed_30d's +1), and after RestartSignup it fell to -1 — the
// confirmation's own contribution to confirmed_30d vanished the instant
// confirmed_at was cleared, but unsubscribed_at was never cleared, so
// unsubscribed_30d's -1 stayed with no matching arrival left in the window.
func TestGrowth30Days_ConfirmUnsubscribeRestartNetsZero(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	since := time.Now().UTC()

	baseline := growth30DaysNet(t, store, since)

	// Create's `now` is real wall-clock time, not offset forward from
	// `since` — Create is what stamps created_at, and List's own ordering
	// (subscribers.created_at DESC, no window filter) would rank a
	// forward-dated row ahead of a genuinely later real-time row created by
	// an unrelated test (TestList_PaginationBoundsAndOrdering) running
	// later in the same package's ~0.5s total suite time. `since`, captured
	// before this call, is still an earlier instant, so `created_at >=
	// since` holds regardless.
	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: 24 * time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(ctx, *created.ConfirmToken, since.Add(2*time.Second)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	afterConfirm := growth30DaysNet(t, store, since)
	if afterConfirm != baseline+1 {
		t.Errorf("net_30d after Confirm = %d, want %d (baseline=%d + 1)", afterConfirm, baseline+1, baseline)
	}

	if _, err := store.Unsubscribe(ctx, created.ID, SourceOneClick, since.Add(3*time.Second)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	afterUnsubscribe := growth30DaysNet(t, store, since)
	if afterUnsubscribe != baseline {
		t.Errorf("net_30d after Unsubscribe = %d, want %d (back to baseline)", afterUnsubscribe, baseline)
	}

	if _, err := store.RestartSignup(ctx, created.ID, RestartSignupInput{ConfirmTTL: 24 * time.Hour}, since.Add(4*time.Second)); err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	afterRestart := growth30DaysNet(t, store, since)
	if afterRestart != baseline {
		t.Errorf("net_30d after RestartSignup = %d, want %d (flat, not baseline-1 — #0324)", afterRestart, baseline)
	}
}

func TestRestartSignup_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.RestartSignup(context.Background(), -1, RestartSignupInput{ConfirmTTL: time.Hour}, time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

// TestRestartSignup_ComplainedStaysComplained is the store-layer proof
// #0025's review explicitly predicted this method would need: RestartSignup
// must consult statusLockedFromNonAdmin exactly like every other status
// mutator in this package, so a complained subscriber can never be
// laundered back to pending (and eventually active) through the "treat as
// new signup" restart path. This is the defense-in-depth backstop for the
// TOCTOU window between the #0026 handler's own status read and this
// UPDATE — see internal/handlers/subscribe_test.go for the end-to-end HTTP
// path proof that the handler itself never calls this method at all when
// it already knows the subscriber is complained.
func TestRestartSignup_ComplainedStaysComplained(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(ctx, *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := store.MarkComplained(ctx, created.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComplained: %v", err)
	}

	result, err := store.RestartSignup(ctx, created.ID, RestartSignupInput{
		SignupIP:   "203.0.113.99",
		ConfirmTTL: time.Hour,
	}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("RestartSignup: %v", err)
	}
	if result.Status != StatusComplained {
		t.Fatalf("Status = %q after RestartSignup on a complained subscriber, want unchanged %q (laundering path must stay closed)", result.Status, StatusComplained)
	}
	if result.ConfirmToken != nil {
		t.Errorf("ConfirmToken = %v, want unchanged nil — RestartSignup must not mint a fresh token for a complained subscriber", result.ConfirmToken)
	}

	// Re-read independently to make sure the guard's effect is durable, not
	// just an artifact of the RETURNING clause.
	final, err := store.FindByEmail(ctx, created.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if final.Status != StatusComplained {
		t.Fatalf("final Status = %q, want %q", final.Status, StatusComplained)
	}
}

// ── List / StatusCounts / GetByID (#0032's admin screen) ────────────────────

func TestGetByID_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	created, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Email != created.Email {
		t.Errorf("Email = %q, want %q", got.Email, created.Email)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	_, err := store.GetByID(context.Background(), -1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestList_FiltersByStatus(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	pending, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	activeSeed, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create active seed: %v", err)
	}
	active, err := store.Confirm(ctx, *activeSeed.ConfirmToken, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	results, total, err := store.List(ctx, ListFilter{Status: StatusActive})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	foundActive := false
	for _, r := range results {
		if r.ID == pending.ID {
			t.Fatalf("List(status=active) returned the pending subscriber %d", pending.ID)
		}
		if r.ID == active.ID {
			foundActive = true
		}
		if r.Status != StatusActive {
			t.Errorf("row %d has status %q, want %q", r.ID, r.Status, StatusActive)
		}
	}
	if !foundActive {
		t.Errorf("List(status=active) did not include the active subscriber %d", active.ID)
	}
	if total < 1 {
		t.Errorf("total = %d, want >= 1", total)
	}
}

func TestList_FiltersByInterest(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	iotaID := seededInterestID(t, pool, "microcontrollers")
	other := seededInterestID(t, pool, "3d-printing")

	withInterest, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetInterests(ctx, withInterest.ID, []int64{iotaID}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}
	without, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetInterests(ctx, without.ID, []int64{other}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	results, _, err := store.List(ctx, ListFilter{InterestID: iotaID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == without.ID {
			t.Fatalf("List(interest_id=%d) returned a subscriber %d that was never linked to it", iotaID, without.ID)
		}
		if r.ID == withInterest.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("List(interest_id=%d) did not include subscriber %d", iotaID, withInterest.ID)
	}
}

func TestList_SearchesByEmailSubstringCaseInsensitive(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	unique := fmt.Sprintf("zz-search-%d", testdb.Unique())
	email := unique + "@Example.COM"
	created, err := store.Create(ctx, NewSignup{Email: email, ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, total, err := store.List(ctx, ListFilter{Query: strings.ToUpper(unique)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].ID != created.ID {
		t.Fatalf("List(q=%q) = %+v (total=%d), want exactly subscriber %d", unique, results, total, created.ID)
	}
}

func TestList_SearchEscapesLikeWildcards(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// "axb" contains no literal underscore. If List's search treated "_" as
	// the ILIKE single-character wildcard instead of escaping it, a query
	// for "a_b" would still match "axb" (any one character in place of the
	// wildcard) — a false positive that leaks membership information ("is
	// there a subscriber matching this shape") beyond an exact substring
	// search. Escaping "_" (and "%") means only a literal underscore can
	// ever match one.
	unique := fmt.Sprintf("zz-under-%d", testdb.Unique())
	email := unique + "-axb@example.com"
	if _, err := store.Create(ctx, NewSignup{Email: email, ConfirmTTL: time.Hour}, time.Now()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, total, err := store.List(ctx, ListFilter{Query: unique + "-a_b"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d for a query containing a literal \"_\" that does not appear in the seeded email, want 0 (wildcard not escaped?)", total)
	}

	// Sanity check: an exact-substring query for the real email DOES match,
	// so the zero result above is proof of escaping, not of List being
	// broken outright.
	_, total, err = store.List(ctx, ListFilter{Query: unique + "-axb"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d for the real seeded email's exact substring, want 1", total)
	}
}

func TestList_PaginationBoundsAndOrdering(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 3; i++ {
		sub, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, time.Now())
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, sub.ID)
		time.Sleep(2 * time.Millisecond) // ensure distinct created_at ordering
	}

	page1, total, err := store.List(ctx, ListFilter{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("len(page1) = %d, want 2", len(page1))
	}
	if total < 3 {
		t.Fatalf("total = %d, want >= 3", total)
	}
	// Newest-first: the most recently created of our three rows should lead.
	if page1[0].ID != ids[2] {
		t.Errorf("page1[0].ID = %d, want %d (newest-first ordering)", page1[0].ID, ids[2])
	}

	page2, _, err := store.List(ctx, ListFilter{Page: 2, PerPage: 2})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	for _, r := range page2 {
		for _, r1 := range page1 {
			if r.ID == r1.ID {
				t.Fatalf("subscriber %d appeared on both page 1 and page 2", r.ID)
			}
		}
	}
}

func TestList_PerPageClampedToMax(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	// A huge per_page must not error and must not panic; List clamps it.
	_, _, err := store.List(context.Background(), ListFilter{PerPage: 1_000_000})
	if err != nil {
		t.Fatalf("List with oversized PerPage: %v", err)
	}
}

func TestStatusCounts_AllFiveStatusesPresentAndAccurate(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	before, err := store.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts (before): %v", err)
	}
	for _, status := range []string{StatusPending, StatusActive, StatusUnsubscribed, StatusBounced, StatusComplained} {
		if _, ok := before[status]; !ok {
			t.Errorf("StatusCounts missing key %q even before any row exists", status)
		}
	}

	pending, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	after, err := store.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts (after): %v", err)
	}
	if after[StatusPending] != before[StatusPending]+1 {
		t.Errorf("pending count = %d, want %d", after[StatusPending], before[StatusPending]+1)
	}
	_ = pending
}

// TestIsolation_UniqueDataNeverCollides is #0091 round two's isolation
// proof. The package no longer truncates before every test (see testPool's
// doc comment) — the table now accumulates rows across the whole binary
// run (and across every prior run of the whole suite against this
// database). Isolation instead rests entirely on every test seeding its
// rows under a value from uniqueEmail(t), unique enough that no two tests',
// or two -count repeats', rows ever share an email.
//
// This test seeds a subscriber under a fresh uniqueEmail(t) and asserts
// exactly one row exists FOR THAT EMAIL — deliberately not a whole-table
// count, which is no longer meaningful once the table isn't truncated per
// test. If uniqueEmail stopped being unique (the actual mechanism holding
// up isolation now), a second seed under the same email would either
// collide with the table's UNIQUE(email) constraint (store.Create returns
// an error) or, if that were somehow bypassed, this count would read 2
// instead of 1.
//
// That failure mode is demonstrated, not just asserted by construction: see
// issues/0091.md's Work log for the output of deliberately hardcoding
// uniqueEmail to return a fixed string and running the package with
// -count=2 — the second iteration's own store.Create fails outright with a
// duplicate-key error, proving uniqueness is load-bearing here rather than
// incidental.
//
// Run with `-count=2 -shuffle=on` to prove the (correct) mechanism holds
// across repeats and across test orderings, not just in single-run
// position-dependent luck.
func TestIsolation_UniqueDataNeverCollides(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	email := uniqueEmail(t)
	if _, err := store.Create(context.Background(), NewSignup{Email: email, ConfirmTTL: time.Hour}, now); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscribers WHERE email = $1`, email,
	).Scan(&count); err != nil {
		t.Fatalf("count subscribers for %s: %v", email, err)
	}
	if count != 1 {
		t.Fatalf("subscribers table has %d rows for the email this test just seeded, want 1 -- "+
			"isolation broken: uniqueEmail collided with another test's (or a prior -count "+
			"iteration's) row", count)
	}
}

// TestGrowth30Days_BoundaryAndSyntheticExclusion is #0061's proof for the
// admin overview dashboard's 30-day growth figure. Like every other test in
// this file, the table is never empty at the start (see testPool's doc
// comment), so this proves the boundary and the synthetic exclusion via
// before/after deltas around a fixed `since` instant, never an absolute
// count.
func TestGrowth30Days_BoundaryAndSyntheticExclusion(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	since := time.Now()

	// A: confirmed BEFORE `since` — must be excluded from the confirmed_30d
	// count no matter when this test runs relative to it. ConfirmTTL is
	// generous (24h, not the usual 1h) so confirm_expires_at (created_at +
	// ConfirmTTL) stays safely after the backdated confirm time below —
	// Confirm requires ConfirmExpiresAt.After(now), and a 1h TTL against a
	// 2h-backdated Create would make expires_at fall before (in fact equal
	// to) the confirm call's own backdated `now`, failing with
	// ErrTokenInvalid for a reason that has nothing to do with what this
	// test is proving.
	a, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: 24 * time.Hour}, since.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if _, err := store.Confirm(ctx, *a.ConfirmToken, since.Add(-time.Hour)); err != nil {
		t.Fatalf("Confirm A: %v", err)
	}

	before, _, _, err := store.Growth30Days(ctx, since)
	if err != nil {
		t.Fatalf("Growth30Days (before): %v", err)
	}

	// B: confirmed AFTER `since` — must be included.
	// Same generous-TTL reasoning as A above: Confirm requires
	// ConfirmExpiresAt strictly AFTER the confirm-time `now`, and a 1h TTL
	// from `since` would make expires_at equal (not after) the since+1h
	// confirm call below.
	b, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: 24 * time.Hour}, since)
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	if _, err := store.Confirm(ctx, *b.ConfirmToken, since.Add(time.Hour)); err != nil {
		t.Fatalf("Confirm B: %v", err)
	}

	// C: a SYNTHETIC subscriber, also confirmed after `since` — must NOT move
	// the count, per #0061's amendment (same exclusion StatusCounts already
	// applies).
	c, err := store.Create(ctx, NewSignup{Email: uniqueEmail(t), ConfirmTTL: 24 * time.Hour, Synthetic: true}, since)
	if err != nil {
		t.Fatalf("Create C (synthetic): %v", err)
	}
	if _, err := store.Confirm(ctx, *c.ConfirmToken, since.Add(time.Hour)); err != nil {
		t.Fatalf("Confirm C: %v", err)
	}

	after, _, _, err := store.Growth30Days(ctx, since)
	if err != nil {
		t.Fatalf("Growth30Days (after): %v", err)
	}
	if after != before+1 {
		t.Errorf("confirmed_30d = %d, want %d (before=%d + B, excluding A's earlier confirm and C's synthetic confirm)", after, before+1, before)
	}

	// A's confirm predates `since` by only an hour; moving the boundary back
	// far enough to include it proves the WHERE clause is an exact
	// comparison, not e.g. always-true.
	includingA, _, _, err := store.Growth30Days(ctx, since.Add(-3*time.Hour))
	if err != nil {
		t.Fatalf("Growth30Days (boundary moved back): %v", err)
	}
	if includingA != after+1 {
		t.Errorf("confirmed_30d with an earlier `since` = %d, want %d (A's confirm should now be in range)", includingA, after+1)
	}

	// Unsubscribed side of the same figure, same before/after shape.
	beforeUnsub, _, unsubBefore, err := store.Growth30Days(ctx, since)
	_ = beforeUnsub
	if err != nil {
		t.Fatalf("Growth30Days (unsub before): %v", err)
	}
	if _, err := store.Unsubscribe(ctx, b.ID, "one_click", since.Add(2*time.Hour)); err != nil {
		t.Fatalf("Unsubscribe B: %v", err)
	}
	_, _, unsubAfter, err := store.Growth30Days(ctx, since)
	if err != nil {
		t.Fatalf("Growth30Days (unsub after): %v", err)
	}
	if unsubAfter != unsubBefore+1 {
		t.Errorf("unsubscribed_30d = %d, want %d", unsubAfter, unsubBefore+1)
	}
}

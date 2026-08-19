package subscribers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to TEST_DATABASE_URL or skips, then truncates the
// subscribers/subscriber_interests tables (both purely structural, unlike
// interests which carries seed data — safe to wipe between tests) so each
// test starts from a clean slate.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}

	truncate(t, pool)
	t.Cleanup(func() {
		truncate(t, pool)
		pool.Close()
	})
	return pool
}

func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate subscriber tables: %v", err)
	}
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
	return fmt.Sprintf("zz-test-%d@example.com", time.Now().UnixNano())
}

func TestCreate_NormalizesEmailAndGeneratesTokens(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	raw := "  Zz-Test-Mixed-Case+ABC@Example.COM  "
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

	wantEmail := "zz-test-mixed-case+abc@example.com"
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

	cases := []struct {
		in   string
		want string
	}{
		{"J.O.H.N.zztest@gmail.com", "j.o.h.n.zztest@gmail.com"}, // dots preserved, only case-folded
		{"jane.zztest+workshops@gmail.com", "jane.zztest+workshops@gmail.com"},
		{"  Spacey.Zztest@Example.com  ", "spacey.zztest@example.com"}, // only trim + lowercase
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

func TestDB_EmailUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	email := uniqueEmail(t)

	if _, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, 'zz-test-raw-token-3')`, email,
	); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ($1, 'zz-test-raw-token-4')`, email)
	if err == nil {
		t.Fatal("second raw insert with duplicate email succeeded; want UNIQUE violation")
	}
}

func TestDB_ManageTokenUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	token := fmt.Sprintf("zz-test-shared-token-%d", time.Now().UnixNano())

	if _, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ('zz-test-a@example.com', $1)`, token,
	); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token) VALUES ('zz-test-b@example.com', $1)`, token)
	if err == nil {
		t.Fatal("second raw insert with duplicate manage_token succeeded; want UNIQUE violation")
	}
}

func TestDB_ConfirmTokenUniqueConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	token := fmt.Sprintf("zz-test-shared-confirm-%d", time.Now().UnixNano())

	if _, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token, confirm_token)
		 VALUES ('zz-test-c@example.com', 'zz-test-raw-token-5', $1)`, token,
	); err != nil {
		t.Fatalf("first raw insert: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO subscribers (email, manage_token, confirm_token)
		 VALUES ('zz-test-d@example.com', 'zz-test-raw-token-6', $1)`, token)
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

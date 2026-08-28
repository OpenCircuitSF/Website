package subscribers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// seedErasureCampaign inserts a throwaway email_campaigns row and returns
// its id, cleaned up at test end (cascading to campaign_interests/
// email_sends via migrations/000017's ON DELETE CASCADE on campaign_id —
// unrelated to the subscriber_id FK this file's tests exercise).
func seedErasureCampaign(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	unique := testdb.Unique()
	name := fmt.Sprintf("zz-erase-test-campaign-%d", unique)
	slug := fmt.Sprintf("zz-erase-test-campaign-%d", unique)
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_campaigns (name, subject, body_md, slug) VALUES ($1, 'zz test subject', 'zz body', $2)
		 RETURNING id`, name, slug,
	).Scan(&id); err != nil {
		t.Fatalf("seed email_campaigns: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, id)
	})
	return id
}

// seedErasureSend inserts one email_sends row for (campaignID, subscriberID)
// at the given status and snapshot email, directly at the SQL layer — this
// package has no Store method over email_sends (internal/mailing owns that
// table's business logic); Erase's own raw SQL against it is exactly what
// this file is testing.
func seedErasureSend(t *testing.T, pool *pgxpool.Pool, campaignID, subscriberID int64, email, status string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_sends (campaign_id, subscriber_id, email, status)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		campaignID, subscriberID, email, status,
	).Scan(&id); err != nil {
		t.Fatalf("seed email_sends (status=%s): %v", status, err)
	}
	return id
}

func emailSendRow(t *testing.T, pool *pgxpool.Pool, sendID int64) (email, status string, subscriberID *int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT email, status, subscriber_id FROM email_sends WHERE id = $1`, sendID,
	).Scan(&email, &status, &subscriberID); err != nil {
		t.Fatalf("read email_sends %d: %v", sendID, err)
	}
	return email, status, subscriberID
}

func TestErase_HardDeletesAndCascadesInterests(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	soldering := seededInterestID(t, pool, "soldering")
	robotics := seededInterestID(t, pool, "robotics")
	if err := store.SetInterests(context.Background(), sub.ID, []int64{soldering, robotics}); err != nil {
		t.Fatalf("SetInterests: %v", err)
	}

	result, err := store.Erase(context.Background(), sub.ID, now)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if result.Email != sub.Email {
		t.Errorf("result.Email = %q, want %q", result.Email, sub.Email)
	}
	if result.PreviousStatus != StatusPending {
		t.Errorf("result.PreviousStatus = %q, want %q", result.PreviousStatus, StatusPending)
	}
	if result.InterestsRemoved != 2 {
		t.Errorf("result.InterestsRemoved = %d, want 2", result.InterestsRemoved)
	}
	if !result.SuppressionAdded {
		t.Error("result.SuppressionAdded = false, want true")
	}
	if result.SuppressionPreexisted {
		t.Error("result.SuppressionPreexisted = true, want false (this address had never been suppressed)")
	}

	if _, err := store.GetByID(context.Background(), sub.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID after Erase: err = %v, want ErrNotFound", err)
	}

	var interestRows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_interests WHERE subscriber_id = $1`, sub.ID,
	).Scan(&interestRows); err != nil {
		t.Fatalf("count subscriber_interests: %v", err)
	}
	if interestRows != 0 {
		t.Errorf("subscriber_interests rows for erased subscriber = %d, want 0 (CASCADE)", interestRows)
	}

	sup := NewSuppressionStore(pool)
	rows, err := sup.ListByEmail(context.Background(), sub.Email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(rows) != 1 || rows[0].Reason != SuppressionReasonManual {
		t.Fatalf("suppressions for erased address = %+v, want exactly one manual row", rows)
	}
}

func TestErase_AnonymizesEmailSendsPreservingCountAndStatus(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two separate campaigns, not one: email_sends carries UNIQUE
	// (campaign_id, subscriber_id) — one subscriber can appear at most once
	// per campaign, so two rows for the SAME subscriber need two campaigns.
	campaignA := seedErasureCampaign(t, pool)
	campaignB := seedErasureCampaign(t, pool)
	sentID := seedErasureSend(t, pool, campaignA, sub.ID, sub.Email, "sent")
	failedID := seedErasureSend(t, pool, campaignB, sub.ID, sub.Email, "failed")

	result, err := store.Erase(context.Background(), sub.ID, now)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if result.EmailSendsAnonymized != 2 {
		t.Errorf("result.EmailSendsAnonymized = %d, want 2", result.EmailSendsAnonymized)
	}

	for _, id := range []int64{sentID, failedID} {
		email, status, subscriberID := emailSendRow(t, pool, id)
		if email == sub.Email {
			t.Errorf("email_sends %d still carries the original address %q after erasure", id, email)
		}
		if subscriberID != nil {
			t.Errorf("email_sends %d subscriber_id = %v, want nil (SET NULL)", id, *subscriberID)
		}
		if id == sentID && status != "sent" {
			t.Errorf("email_sends %d status = %q, want unchanged %q", id, status, "sent")
		}
		if id == failedID && status != "failed" {
			t.Errorf("email_sends %d status = %q, want unchanged %q", id, status, "failed")
		}
	}

	// The row count itself must be unchanged — anonymized, never deleted, so
	// per-campaign send counts (#0049) never silently change.
	var countA, countB int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_sends WHERE campaign_id = $1`, campaignA,
	).Scan(&countA); err != nil {
		t.Fatalf("count email_sends (campaign A): %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_sends WHERE campaign_id = $1`, campaignB,
	).Scan(&countB); err != nil {
		t.Fatalf("count email_sends (campaign B): %v", err)
	}
	if countA != 1 || countB != 1 {
		t.Errorf("email_sends row counts = %d/%d, want 1/1 (anonymized, not deleted)", countA, countB)
	}
}

func TestErase_RefusesWhilePendingSend(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	campaignID := seedErasureCampaign(t, pool)
	queuedID := seedErasureSend(t, pool, campaignID, sub.ID, sub.Email, "queued")

	if _, err := store.Erase(context.Background(), sub.ID, now); !errors.Is(err, ErrHasPendingSends) {
		t.Fatalf("Erase with a queued send: err = %v, want ErrHasPendingSends", err)
	}

	// Nothing mutated: the subscriber survives, and the queued row is
	// untouched (still queued, still pointing at the live subscriber).
	if _, err := store.GetByID(context.Background(), sub.ID); err != nil {
		t.Errorf("GetByID after refused Erase: %v, want the subscriber to still exist", err)
	}
	email, status, subscriberID := emailSendRow(t, pool, queuedID)
	if status != "queued" {
		t.Errorf("email_sends status = %q after refused Erase, want unchanged %q", status, "queued")
	}
	if email != sub.Email {
		t.Errorf("email_sends email = %q after refused Erase, want unchanged %q", email, sub.Email)
	}
	if subscriberID == nil || *subscriberID != sub.ID {
		t.Errorf("email_sends subscriber_id after refused Erase = %v, want %d", subscriberID, sub.ID)
	}

	// 'sending' (the worker's in-flight claim state) refuses identically.
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_sends SET status = 'sending' WHERE id = $1`, queuedID,
	); err != nil {
		t.Fatalf("force status=sending: %v", err)
	}
	if _, err := store.Erase(context.Background(), sub.ID, now); !errors.Is(err, ErrHasPendingSends) {
		t.Fatalf("Erase with a sending send: err = %v, want ErrHasPendingSends", err)
	}
}

func TestErase_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Erase(context.Background(), sub.ID, now); err != nil {
		t.Fatalf("first Erase: %v", err)
	}
	// A repeat call against the now-deleted id — not a hardcoded/seeded id
	// (CLAUDE.md §8b), the same real id this test just created and removed.
	if _, err := store.Erase(context.Background(), sub.ID, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Erase: err = %v, want ErrNotFound", err)
	}
}

// TestErase_PreservesAndAddsToExistingSuppressions is the direct proof for
// this issue's central design tension (CLAUDE.md §9: `complained` never
// auto-resubscribes). A subscriber that already carries a `complaint`
// suppression row (the shape #0038's SES ingestion writes alongside a
// status transition to complained) keeps that row untouched by Erase, which
// adds its own independent `manual` row on top — so the address ends up
// MORE blocked, never less, and no reason is ever silently discarded.
func TestErase_PreservesAndAddsToExistingSuppressions(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	sup := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := sup.Add(context.Background(), NewSuppression{
		Email: sub.Email, Reason: SuppressionReasonComplaint, Note: "SES complaint",
	}, now); err != nil {
		t.Fatalf("seed complaint suppression: %v", err)
	}

	result, err := store.Erase(context.Background(), sub.ID, now)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if result.SuppressionPreexisted {
		t.Error("result.SuppressionPreexisted = true, want false (only a COMPLAINT row pre-existed, not a MANUAL one)")
	}

	rows, err := sup.ListByEmail(context.Background(), sub.Email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	reasons := map[string]bool{}
	for _, r := range rows {
		reasons[r.Reason] = true
	}
	if len(rows) != 2 || !reasons[SuppressionReasonComplaint] || !reasons[SuppressionReasonManual] {
		t.Fatalf("suppressions for erased address = %+v, want both complaint (preserved) and manual (added)", rows)
	}

	if suppressed, err := sup.IsSuppressed(context.Background(), sub.Email); err != nil || !suppressed {
		t.Errorf("IsSuppressed(%q) = %v, %v, want true, nil — the property this issue exists to guarantee: the address stays blocked after the subscriber row is gone", sub.Email, suppressed, err)
	}
}

// TestErase_RepeatManualSuppressionIsIdempotent proves an erasure request
// against an address already manually suppressed (e.g. a prior admin
// Suppress action, or a retried erasure request that failed after the
// suppression write but before the delete — see Erase's transaction) does
// not error and does not create a second manual row.
func TestErase_RepeatManualSuppressionIsIdempotent(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	sup := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := sup.Add(context.Background(), NewSuppression{
		Email: sub.Email, Reason: SuppressionReasonManual, Note: "admin suppress, pre-erasure",
	}, now); err != nil {
		t.Fatalf("seed manual suppression: %v", err)
	}

	result, err := store.Erase(context.Background(), sub.ID, now)
	if err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if !result.SuppressionPreexisted {
		t.Error("result.SuppressionPreexisted = false, want true (a manual row already existed)")
	}

	rows, err := sup.ListByEmail(context.Background(), sub.Email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("suppressions for erased address = %+v, want exactly one row (no duplicate)", rows)
	}
}

// TestErase_RedactsSubscriberEvents_PreservesRows is the phase-3 review's
// defect 1 regression test (issues/0126.md, "Bounced 2026-08-24"). The old
// redaction in erase.go — matching only WHERE subscriber_id = the id being
// erased — never caught the subscriber_events rows suppressions.go writes
// with SubscriberID left nil (ActionSuppressed,
// ActionUnsuppressed — both keyed by email, not subscriber id, by that
// package's own design). Erase always adds a `manual` suppression before
// redacting, so EVERY erasure wrote and then failed to redact one of these
// itself; a PRE-EXISTING suppressed/unsuppressed row for the address (a
// prior hard bounce, complaint, or admin action — exercised here) leaked
// the same way. Proved by execution in the review: Create -> Erase left
// one row still holding the real address, which made
// PrivacyPolicy.svelte's "the placeholder no longer identifies you" claim
// false. This test seeds exactly that pre-existing case — a suppression
// (and its `suppressed` event) added BEFORE the subscriber ever signs up —
// and asserts zero subscriber_events rows hold the real address after
// Erase, while the rows themselves (redacted) survive.
func TestErase_RedactsSubscriberEvents_PreservesRows(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	sup := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	email := uniqueEmail(t)

	// Pre-existing suppression for the address, BEFORE any subscribers row
	// exists for it — suppressions.go's addSuppression writes this
	// subscriber_events row with SubscriberID nil (the exact shape the old
	// predicate missed), and Erase's own addSuppression call (the
	// `manual` reason it always adds) writes a SECOND one identically
	// shaped, seconds later, inside Erase itself.
	if _, err := sup.Add(context.Background(), NewSuppression{
		Email: email, Reason: SuppressionReasonComplaint, Note: "SES complaint, pre-signup",
	}, now); err != nil {
		t.Fatalf("seed pre-existing complaint suppression: %v", err)
	}

	sub, err := store.Create(context.Background(), NewSignup{Email: email, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Before erasing: at least the pre-existing `suppressed` event
	// (subscriber_id NULL) and the `signup_requested` event (subscriber_id
	// = sub.ID) both hold the real, normalized address.
	var beforeCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1`, sub.Email,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("counting subscriber_events before erase: %v", err)
	}
	if beforeCount < 2 {
		t.Fatalf("subscriber_events rows for %q before erase = %d, want >= 2 (test setup invalid)", sub.Email, beforeCount)
	}

	if _, err := store.Erase(context.Background(), sub.ID, now); err != nil {
		t.Fatalf("Erase: %v", err)
	}

	// The load-bearing assertion: no row anywhere holds the real address
	// after erasure, regardless of how it was linked (by subscriber_id or
	// by email alone).
	var leaked int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1`, sub.Email,
	).Scan(&leaked); err != nil {
		t.Fatalf("counting subscriber_events after erase: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("subscriber_events rows still holding the real address after erase = %d, want 0 (defect 1 regression)", leaked)
	}

	// The rows themselves are PRESERVED, not deleted — redacted (email
	// replaced by the subscriber-id-keyed placeholder) and, for the
	// subscriber_id-linked rows, subscriber_id nulled by the DELETE's own
	// ON DELETE SET NULL FK. This is the erasure's own evidence, per PRD
	// §6.11 and PrivacyPolicy.svelte's fifth retained item.
	//
	// wantAfter = beforeCount (the rows already counted above, now
	// redacted) + 1 (the `manual` suppression's OWN `suppressed` event,
	// which Erase writes seconds before the redaction UPDATE runs — the
	// exact row defect 1 was about — so it exists only after Erase starts
	// and is not part of beforeCount) + 1 (the `erased` event itself,
	// written directly with the placeholder, never holding the real
	// address at all).
	placeholder := erasedEventPlaceholder(sub.ID)
	wantAfter := beforeCount + 2
	var afterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1`, placeholder,
	).Scan(&afterCount); err != nil {
		t.Fatalf("counting redacted subscriber_events rows: %v", err)
	}
	if afterCount != wantAfter {
		t.Fatalf("redacted subscriber_events rows for placeholder %q = %d, want %d (beforeCount=%d rows redacted, plus Erase's own manual-suppression event, plus the erased event itself)", placeholder, afterCount, wantAfter, beforeCount)
	}

	var stillLinked int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1`, sub.ID,
	).Scan(&stillLinked); err != nil {
		t.Fatalf("counting subscriber_id-linked rows after erase: %v", err)
	}
	if stillLinked != 0 {
		t.Fatalf("subscriber_events rows still carrying subscriber_id after erase = %d, want 0 (DELETE's ON DELETE SET NULL should have nulled it)", stillLinked)
	}
}

// TestErase_RedactsSubscriberEvents_NormalizesMixedCaseWhitespacePaddedAddress
// is #0266 item 1: TestErase_RedactsSubscriberEvents_PreservesRows above
// uses uniqueEmail(), which is already lower-cased and unpadded, so it never
// exercises the `lower(trim($3))` normalization erase.go's redaction
// predicate depends on — it would pass identically if that call were
// deleted from the SQL entirely. #0126's phase-3 re-review wrote the real
// probe (issues/0126.md, "Blocker 1"): submit the address mixed-case and
// whitespace-padded at every entry point that can write a NULL-subscriber_id
// subscriber_events row — SuppressionStore.Add, SuppressionStore.Remove, and
// Store.Create — then erase, then hunt for a leak three ways (exact,
// case-insensitive, substring on the local part), plus census the `detail`
// JSONB column directly. This commits that probe as a real test rather than
// leaving it as a one-off review artifact.
//
// The raw address is fed to Add/Remove/Create verbatim, never
// pre-normalized by the test itself — the whole point is to prove the
// SQL-level lower(trim(...)) call is what's doing the work, not the test's
// own setup accidentally already matching.
func TestErase_RedactsSubscriberEvents_NormalizesMixedCaseWhitespacePaddedAddress(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	sup := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	raw := fmt.Sprintf("  ZZ-Review-%d@Example.COM  ", testdb.Unique())
	normalized := strings.ToLower(strings.TrimSpace(raw))
	localPart := strings.SplitN(normalized, "@", 2)[0]

	// Entry point 1: a pre-existing suppression, BEFORE any subscribers row
	// exists — writes a `suppressed` event with subscriber_id NULL (the
	// exact shape #0126's defect 1 missed).
	if _, err := sup.Add(context.Background(), NewSuppression{
		Email: raw, Reason: SuppressionReasonComplaint, Note: "mixed-case, whitespace-padded, pre-signup",
	}, now); err != nil {
		t.Fatalf("seed pre-existing complaint suppression: %v", err)
	}

	// Entry point 2: Remove, exercising ActionUnsuppressed — the second
	// NULL-subscriber_id action type — using the same raw, unnormalized
	// address.
	if _, err := sup.Remove(context.Background(), raw, SuppressionReasonComplaint); err != nil {
		t.Fatalf("remove complaint suppression: %v", err)
	}

	// Re-add so the address is suppressed again going into Create/Erase,
	// matching the reviewer's probe (both NULL-subscriber_id action types
	// exercised, address left suppressed throughout — same as
	// TestErase_RedactsSubscriberEvents_PreservesRows above).
	if _, err := sup.Add(context.Background(), NewSuppression{
		Email: raw, Reason: SuppressionReasonComplaint, Note: "mixed-case, whitespace-padded, re-added",
	}, now); err != nil {
		t.Fatalf("re-add complaint suppression: %v", err)
	}

	// Entry point 3: Create — the raw address, still unnormalized. store.go's
	// INSERT applies its own lower(trim(...)) and returns the DB-normalized
	// value, which is what the rest of this test (and Erase's own $3) reads
	// back — proving Create's write path normalizes independently of this
	// test having done it by hand.
	sub, err := store.Create(context.Background(), NewSignup{Email: raw, ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.Email != normalized {
		t.Fatalf("Create did not normalize the raw address: got %q, want %q", sub.Email, normalized)
	}

	var beforeCount, beforeNullSubID int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1`, normalized,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("counting subscriber_events before erase: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1 AND subscriber_id IS NULL`, normalized,
	).Scan(&beforeNullSubID); err != nil {
		t.Fatalf("counting NULL-subscriber_id subscriber_events before erase: %v", err)
	}
	// suppressed, unsuppressed, suppressed (NULL subscriber_id) + signup_requested (subscriber_id = sub.ID) = 4 minimum.
	if beforeCount < 4 || beforeNullSubID < 3 {
		t.Fatalf("subscriber_events before erase = %d (nullSubID=%d), want >= 4 (nullSubID >= 3) — test setup invalid", beforeCount, beforeNullSubID)
	}

	if _, err := store.Erase(context.Background(), sub.ID, now); err != nil {
		t.Fatalf("Erase: %v", err)
	}

	// Leak hunted three ways, per #0126's review-only probe — none may find
	// a row.
	assertNoRowsMatch := func(label, query string, args ...any) {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if n != 0 {
			t.Fatalf("%s: found %d row(s) still holding the address, want 0", label, n)
		}
	}
	assertNoRowsMatch("exact match",
		`SELECT count(*) FROM subscriber_events WHERE email = $1`, normalized)
	assertNoRowsMatch("case-insensitive match",
		`SELECT count(*) FROM subscriber_events WHERE lower(email) = lower($1)`, normalized)
	assertNoRowsMatch("substring on local part",
		`SELECT count(*) FROM subscriber_events WHERE email ILIKE '%' || $1 || '%'`, localPart)
	// The `detail` JSONB column: suppressed/unsuppressed events store
	// {"reason": ...}, never the address, but census it directly rather
	// than taking that on report — a future write site adding the address
	// to Detail would leak here even with the email column itself clean.
	assertNoRowsMatch("detail JSONB",
		`SELECT count(*) FROM subscriber_events WHERE detail::text ILIKE '%' || $1 || '%'`, localPart)

	// The rows are preserved, redacted — same shape as
	// TestErase_RedactsSubscriberEvents_PreservesRows above.
	placeholder := erasedEventPlaceholder(sub.ID)
	var afterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1`, placeholder,
	).Scan(&afterCount); err != nil {
		t.Fatalf("counting redacted subscriber_events rows: %v", err)
	}
	// beforeCount rows redacted + the manual suppression's own `suppressed`
	// event (written seconds before the redaction UPDATE inside Erase) +
	// the `erased` event itself.
	wantAfter := beforeCount + 2
	if afterCount != wantAfter {
		t.Fatalf("redacted subscriber_events rows for placeholder %q = %d, want %d", placeholder, afterCount, wantAfter)
	}
}

// TestErase_RefusesWhileOutboundQueued proves Erase's outbound_queue check
// (erase.go, "Same refusal, extended to outbound_queue") refuses erasure
// while a row is genuinely CLAIMED ('sending') — a worker mid-send — but
// NOT while one is merely 'queued', since #0126, every non-synthetic
// Create() leaves exactly such a row until the outbox worker sends it, and
// blocking on 'queued' would make ordinary GDPR erasure of a subscriber
// who has not yet had their confirmation delivered impossible (see
// erase.go's own comment on this departure from the plan's literal text).
func TestErase_RefusesWhileOutboundQueued(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create() leaves the confirmation row 'queued' — Erase must succeed
	// anyway (the narrower, deliberate scope).
	if _, err := store.Erase(context.Background(), sub.ID, now); err != nil {
		t.Fatalf("Erase with a merely 'queued' outbound_queue row = %v, want success", err)
	}

	// Now the 'sending' (claimed) case, which must refuse. A fresh
	// subscriber, since the first was just erased.
	sub2, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create (2nd): %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE outbound_queue SET status = 'sending', claimed_at = now() WHERE subscriber_id = $1`, sub2.ID,
	); err != nil {
		t.Fatalf("forcing outbound_queue row to 'sending': %v", err)
	}

	if _, err := store.Erase(context.Background(), sub2.ID, now); !errors.Is(err, ErrHasPendingSends) {
		t.Fatalf("Erase with a 'sending' outbound_queue row = %v, want %v", err, ErrHasPendingSends)
	}
}

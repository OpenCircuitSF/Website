package mailing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// ── helpers (CLAUDE.md §8b: every test seeds its own throwaway rows, never
// targets a literal or seeded id) ────────────────────────────────────────────

func uniqueMailingSubscriberEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-mailing-audience-%d@example.com", time.Now().UnixNano())
}

// seedSubscriber inserts a minimal subscribers row directly at the given
// status (bypassing subscribers.Store.Create, which only ever produces
// pending rows), cleaned up at test end.
func seedSubscriber(t *testing.T, pool *pgxpool.Pool, status string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO subscribers (email, status, manage_token) VALUES ($1, $2, $3) RETURNING id`,
		uniqueMailingSubscriberEmail(t), status, fmt.Sprintf("zz-mailing-mtok-%d", time.Now().UnixNano()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed subscriber at status %q: %v", status, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, id)
	})
	return id
}

// attachInterest inserts a subscriber_interests row directly.
func attachInterest(t *testing.T, pool *pgxpool.Pool, subscriberID, interestID int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)`,
		subscriberID, interestID,
	); err != nil {
		t.Fatalf("attach interest %d to subscriber %d: %v", interestID, subscriberID, err)
	}
}

// seedSuppression inserts a suppressions row directly for email/reason,
// cleaned up at test end.
func seedSuppression(t *testing.T, pool *pgxpool.Pool, email, reason string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO suppressions (email, reason) VALUES (lower(trim($1)), $2)
		 ON CONFLICT (email, reason) DO NOTHING`,
		email, reason,
	); err != nil {
		t.Fatalf("seed suppression for %q reason %q: %v", email, reason, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM suppressions WHERE email = lower(trim($1)) AND reason = $2`, email, reason)
	})
}

// materializedSubscriberIDs returns the subscriber_id set email_sends
// carries for campaignID, for asserting exactly which subscribers a
// Materialize call selected.
func materializedSubscriberIDs(t *testing.T, pool *pgxpool.Pool, campaignID int64) []int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT subscriber_id FROM email_sends WHERE campaign_id = $1 ORDER BY subscriber_id`, campaignID)
	if err != nil {
		t.Fatalf("list materialized subscriber ids: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan materialized subscriber id: %v", err)
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func assertIDSet(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		t.Fatalf("id set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("id set = %v, want %v", got, want)
		}
	}
}

func newMailingCampaign(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	return c.ID
}

// ── Materialize: the four audience modes (PRD §6.6, plan §4) ────────────────

func TestAudienceStore_Materialize_FourModes(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	interestA := seedInterest(t, pool)
	interestB := seedInterest(t, pool)

	subNone := seedSubscriber(t, pool, subscribers.StatusActive)
	subA := seedSubscriber(t, pool, subscribers.StatusActive)
	attachInterest(t, pool, subA, interestA.ID)
	subB := seedSubscriber(t, pool, subscribers.StatusActive)
	attachInterest(t, pool, subB, interestB.ID)
	subAB := seedSubscriber(t, pool, subscribers.StatusActive)
	attachInterest(t, pool, subAB, interestA.ID)
	attachInterest(t, pool, subAB, interestB.ID)

	cases := []struct {
		name string
		aud  Audience
		want []int64
	}{
		{"all", Audience{Mode: AudienceAll}, []int64{subNone, subA, subB, subAB}},
		{"any_of", Audience{Mode: AudienceAnyOf, InterestIDs: []int64{interestA.ID}}, []int64{subA, subAB}},
		{"all_of", Audience{Mode: AudienceAllOf, InterestIDs: []int64{interestA.ID, interestB.ID}}, []int64{subAB}},
		{"none_selected", Audience{Mode: AudienceNoneSelected}, []int64{subNone}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			campaignID := newMailingCampaign(t, pool)
			if _, err := store.Materialize(ctx, campaignID, tc.aud); err != nil {
				t.Fatalf("Materialize(%s): %v", tc.name, err)
			}
			assertIDSet(t, materializedSubscriberIDs(t, pool, campaignID), tc.want...)
		})
	}
}

// ── Materialize: status exclusions, exhaustive (plan §3, §10.2) ─────────────

func TestAudienceStore_Materialize_OnlyActiveIncluded(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	pending := seedSubscriber(t, pool, subscribers.StatusPending)
	active := seedSubscriber(t, pool, subscribers.StatusActive)
	unsubscribed := seedSubscriber(t, pool, subscribers.StatusUnsubscribed)
	bounced := seedSubscriber(t, pool, subscribers.StatusBounced)
	complained := seedSubscriber(t, pool, subscribers.StatusComplained)

	campaignID := newMailingCampaign(t, pool)
	if _, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	got := materializedSubscriberIDs(t, pool, campaignID)
	assertIDSet(t, got, active)

	contains := func(id int64) bool {
		for _, g := range got {
			if g == id {
				return true
			}
		}
		return false
	}
	if contains(pending) {
		t.Error("a pending subscriber was materialized; double opt-in requires confirmed consent")
	}
	if contains(complained) {
		t.Error("a complained subscriber was materialized; CLAUDE.md §9 makes that state terminal")
	}
	if contains(unsubscribed) {
		t.Error("an unsubscribed subscriber was materialized")
	}
	if contains(bounced) {
		t.Error("a bounced subscriber was materialized")
	}
}

// ── Materialize: suppression exclusion is reason-blind (plan §2) ────────────

func TestAudienceStore_Materialize_SuppressionExclusion_ReasonBlind(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	reasons := []string{
		subscribers.SuppressionReasonHardBounce,
		subscribers.SuppressionReasonComplaint,
		subscribers.SuppressionReasonManual,
		subscribers.SuppressionReasonRepeatedSoftBounce,
	}

	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			active := seedSubscriber(t, pool, subscribers.StatusActive)
			var email string
			if err := pool.QueryRow(ctx, `SELECT email FROM subscribers WHERE id = $1`, active).Scan(&email); err != nil {
				t.Fatalf("read subscriber email: %v", err)
			}
			seedSuppression(t, pool, email, reason)

			campaignID := newMailingCampaign(t, pool)
			if _, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll}); err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			got := materializedSubscriberIDs(t, pool, campaignID)
			for _, g := range got {
				if g == active {
					t.Errorf("subscriber suppressed for reason %q was still materialized", reason)
				}
			}
		})
	}
}

// ── Materialize: safely re-runnable (plan §6, the resumability criterion) ──

func TestAudienceStore_Materialize_ReRunIsIdempotent(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	seedSubscriber(t, pool, subscribers.StatusActive)
	seedSubscriber(t, pool, subscribers.StatusActive)
	campaignID := newMailingCampaign(t, pool)

	first, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll})
	if err != nil {
		t.Fatalf("first Materialize: %v", err)
	}
	if first.Inserted != 2 {
		t.Fatalf("first Materialize.Inserted = %d, want 2", first.Inserted)
	}

	// Mark one row 'sent' by its captured id — the row an ordinary re-run
	// must never touch.
	var sentID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_sends WHERE campaign_id = $1 ORDER BY id LIMIT 1`, campaignID,
	).Scan(&sentID); err != nil {
		t.Fatalf("find a materialized row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE email_sends SET status = 'sent', sent_at = now() WHERE id = $1`, sentID,
	); err != nil {
		t.Fatalf("mark row sent: %v", err)
	}
	var sentAtBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT sent_at FROM email_sends WHERE id = $1`, sentID).Scan(&sentAtBefore); err != nil {
		t.Fatalf("read sent_at: %v", err)
	}

	second, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll})
	if err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
	if second.Inserted != 0 {
		t.Errorf("second Materialize.Inserted = %d, want 0 (re-run must not duplicate or resurrect rows)", second.Inserted)
	}

	var count int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_sends WHERE campaign_id = $1`, campaignID).Scan(&count); err != nil {
		t.Fatalf("count email_sends: %v", err)
	}
	if count != 2 {
		t.Errorf("email_sends row count = %d, want 2 (unchanged)", count)
	}

	var statusAfter string
	var sentAtAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT status, sent_at FROM email_sends WHERE id = $1`, sentID).Scan(&statusAfter, &sentAtAfter); err != nil {
		t.Fatalf("read row after re-run: %v", err)
	}
	if statusAfter != "sent" {
		t.Errorf("status after re-run = %q, want %q (re-run must never reset a sent row to queued)", statusAfter, "sent")
	}
	if !sentAtAfter.Equal(sentAtBefore) {
		t.Errorf("sent_at changed across re-run: before=%v after=%v", sentAtBefore, sentAtAfter)
	}
}

// ── Materialize + Recheck: the test the whole design hangs on (plan §5) ────

func TestAudienceStore_RecheckEligible_SuppressedAfterMaterialization(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	active := seedSubscriber(t, pool, subscribers.StatusActive)
	var email string
	if err := pool.QueryRow(ctx, `SELECT email FROM subscribers WHERE id = $1`, active).Scan(&email); err != nil {
		t.Fatalf("read subscriber email: %v", err)
	}
	campaignID := newMailingCampaign(t, pool)
	if _, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	var sendID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_sends WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, active,
	).Scan(&sendID); err != nil {
		t.Fatalf("find materialized row: %v", err)
	}

	// Now suppress the address after materialization — this is the
	// staleness the snapshot cannot self-correct, per the package doc
	// comment.
	seedSuppression(t, pool, email, subscribers.SuppressionReasonManual)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	recipients, skipped, err := store.RecheckEligibleTx(ctx, tx, campaignID, []int64{sendID})
	if err != nil {
		t.Fatalf("RecheckEligibleTx: %v", err)
	}
	if len(recipients) != 0 {
		t.Errorf("recipients = %v, want empty (address is suppressed)", recipients)
	}
	if len(skipped) != 1 || skipped[0].SendID != sendID {
		t.Fatalf("skipped = %+v, want exactly the suppressed send", skipped)
	}

	n, err := store.MarkSkippedTx(ctx, tx, campaignID, []int64{sendID}, skipped[0].Reason)
	if err != nil {
		t.Fatalf("MarkSkippedTx: %v", err)
	}
	if n != 1 {
		t.Errorf("MarkSkippedTx rows affected = %d, want 1", n)
	}

	// A second recheck of the same (now-skipped) row must return it in
	// NEITHER list — it already left 'queued'.
	recipients2, skipped2, err := store.RecheckEligibleTx(ctx, tx, campaignID, []int64{sendID})
	if err != nil {
		t.Fatalf("second RecheckEligibleTx: %v", err)
	}
	if len(recipients2) != 0 || len(skipped2) != 0 {
		t.Errorf("second recheck = recipients:%v skipped:%v, want both empty", recipients2, skipped2)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM email_sends WHERE id = $1`, sendID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "skipped" {
		t.Errorf("status = %q, want %q", status, "skipped")
	}
}

func TestAudienceStore_RecheckEligible_UnsubscribedAfterMaterialization(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	active := seedSubscriber(t, pool, subscribers.StatusActive)
	campaignID := newMailingCampaign(t, pool)
	if _, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	var sendID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_sends WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, active,
	).Scan(&sendID); err != nil {
		t.Fatalf("find materialized row: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE subscribers SET status = $2 WHERE id = $1`, active, subscribers.StatusUnsubscribed,
	); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	recipients, skipped, err := store.RecheckEligibleTx(ctx, tx, campaignID, []int64{sendID})
	if err != nil {
		t.Fatalf("RecheckEligibleTx: %v", err)
	}
	if len(recipients) != 0 {
		t.Errorf("recipients = %v, want empty (subscriber unsubscribed)", recipients)
	}
	if len(skipped) != 1 || skipped[0].SendID != sendID {
		t.Fatalf("skipped = %+v, want exactly the unsubscribed send", skipped)
	}
}

// ── Preview and Materialize agree (plan §4, "share the query") ─────────────

func TestAudienceStore_PreviewMatchesMaterializeCount(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	interestA := seedInterest(t, pool)
	interestB := seedInterest(t, pool)
	seedSubscriber(t, pool, subscribers.StatusActive) // no interests
	sub2 := seedSubscriber(t, pool, subscribers.StatusActive)
	attachInterest(t, pool, sub2, interestA.ID)
	sub3 := seedSubscriber(t, pool, subscribers.StatusActive)
	attachInterest(t, pool, sub3, interestA.ID)
	attachInterest(t, pool, sub3, interestB.ID)
	seedSubscriber(t, pool, subscribers.StatusPending) // must never count

	modes := []Audience{
		{Mode: AudienceAll},
		{Mode: AudienceAnyOf, InterestIDs: []int64{interestA.ID}},
		{Mode: AudienceAllOf, InterestIDs: []int64{interestA.ID, interestB.ID}},
		{Mode: AudienceNoneSelected},
	}

	for _, aud := range modes {
		t.Run(aud.Mode, func(t *testing.T) {
			preview, err := store.Preview(ctx, aud, 0)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			campaignID := newMailingCampaign(t, pool)
			result, err := store.Materialize(ctx, campaignID, aud)
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			if result.Inserted != preview.Count {
				t.Errorf("Materialize.Inserted = %d, Preview.Count = %d, want equal", result.Inserted, preview.Count)
			}
		})
	}
}

// ── Preview writes nothing ──────────────────────────────────────────────────

func TestAudienceStore_Preview_WritesNothing(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	seedSubscriber(t, pool, subscribers.StatusActive)

	var before int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_sends`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	if _, err := store.Preview(ctx, Audience{Mode: AudienceAll}, 20); err != nil {
		t.Fatalf("Preview: %v", err)
	}

	var after int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_sends`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if before != after {
		t.Errorf("email_sends count changed from %d to %d; Preview must never write", before, after)
	}
}

// ── Chunking, and the all-conflicts cursor-advance regression (plan §8) ────

func TestAudienceStore_Materialize_Chunking(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	store.chunkSize = 2
	ctx := context.Background()

	for i := 0; i < 7; i++ {
		seedSubscriber(t, pool, subscribers.StatusActive)
	}
	campaignID := newMailingCampaign(t, pool)

	result, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if result.Inserted != 7 {
		t.Errorf("Inserted = %d, want 7", result.Inserted)
	}
	if result.Chunks <= 1 {
		t.Errorf("Chunks = %d, want > 1 with chunkSize=2 over 7 rows", result.Chunks)
	}
}

// TestAudienceStore_Materialize_AllConflictsChunkStillAdvancesCursor is the
// named regression test for the trap materializeChunkQuery's doc comment
// describes: RETURNING only yields inserted rows, so if the cursor advanced
// from RETURNING instead of from the scanned page, a re-run whose entire
// chunk conflicts (every row already materialized) would return zero rows,
// never move the cursor, and loop forever. Re-materializing an
// already-complete campaign with a tiny chunk size must terminate.
func TestAudienceStore_Materialize_AllConflictsChunkStillAdvancesCursor(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	store.chunkSize = 2
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		seedSubscriber(t, pool, subscribers.StatusActive)
	}
	campaignID := newMailingCampaign(t, pool)

	first, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll})
	if err != nil {
		t.Fatalf("first Materialize: %v", err)
	}
	if first.Inserted != 5 {
		t.Fatalf("first Materialize.Inserted = %d, want 5", first.Inserted)
	}

	done := make(chan struct {
		result MaterializeResult
		err    error
	}, 1)
	go func() {
		result, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll})
		done <- struct {
			result MaterializeResult
			err    error
		}{result, err}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("second Materialize: %v", out.err)
		}
		if out.result.Inserted != 0 {
			t.Errorf("second Materialize.Inserted = %d, want 0", out.result.Inserted)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("re-materializing an already-complete campaign did not terminate within 10s — the cursor is not advancing on an all-conflicts chunk")
	}
}

// ── Empty interest set: the all_of-becomes-all guard (plan §4) ─────────────

func TestAudienceStore_EmptyInterestSetRejected(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	seedSubscriber(t, pool, subscribers.StatusActive)

	for _, mode := range []string{AudienceAnyOf, AudienceAllOf} {
		t.Run(mode, func(t *testing.T) {
			aud := Audience{Mode: mode} // no interests

			if _, err := store.Preview(ctx, aud, 0); !errors.Is(err, ErrAudienceInterestsRequired) {
				t.Errorf("Preview error = %v, want ErrAudienceInterestsRequired", err)
			}

			campaignID := newMailingCampaign(t, pool)
			if _, err := store.Materialize(ctx, campaignID, aud); !errors.Is(err, ErrAudienceInterestsRequired) {
				t.Errorf("Materialize error = %v, want ErrAudienceInterestsRequired", err)
			}
			var count int64
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_sends WHERE campaign_id = $1`, campaignID).Scan(&count); err != nil {
				t.Fatalf("count email_sends: %v", err)
			}
			if count != 0 {
				t.Errorf("email_sends rows written despite ErrAudienceInterestsRequired: %d", count)
			}
		})
	}
}

// ── Ignored interests warn, rather than reject (plan §4) ────────────────────

func TestAudienceStore_IgnoredInterestsWarn(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	interest := seedInterest(t, pool)
	seedSubscriber(t, pool, subscribers.StatusActive)

	for _, mode := range []string{AudienceAll, AudienceNoneSelected} {
		t.Run(mode, func(t *testing.T) {
			preview, err := store.Preview(ctx, Audience{Mode: mode, InterestIDs: []int64{interest.ID}}, 0)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if len(preview.Warnings) == 0 {
				t.Errorf("Warnings is empty, want a warning about ignored interests for mode %q", mode)
			}
		})
	}
}

// ── ManageToken is read fresh, not from the materialization snapshot ───────

func TestAudienceStore_RecheckEligible_ManageTokenIsFresh(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	active := seedSubscriber(t, pool, subscribers.StatusActive)
	campaignID := newMailingCampaign(t, pool)
	if _, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	var sendID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_sends WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, active,
	).Scan(&sendID); err != nil {
		t.Fatalf("find materialized row: %v", err)
	}

	newToken := fmt.Sprintf("zz-mailing-rotated-%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, `UPDATE subscribers SET manage_token = $2 WHERE id = $1`, active, newToken); err != nil {
		t.Fatalf("rotate manage_token: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	recipients, _, err := store.RecheckEligibleTx(ctx, tx, campaignID, []int64{sendID})
	if err != nil {
		t.Fatalf("RecheckEligibleTx: %v", err)
	}
	if len(recipients) != 1 {
		t.Fatalf("recipients = %v, want exactly one", recipients)
	}
	if recipients[0].ManageToken != newToken {
		t.Errorf("ManageToken = %q, want the freshly-rotated %q", recipients[0].ManageToken, newToken)
	}
}

// ── Snapshot/live divergence (plan §7's "we mail the address we checked") ──

func TestAudienceStore_RecheckEligible_SnapshotEmailDivergesFromLive(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	active := seedSubscriber(t, pool, subscribers.StatusActive)
	campaignID := newMailingCampaign(t, pool)
	if _, err := store.Materialize(ctx, campaignID, Audience{Mode: AudienceAll}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	var sendID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM email_sends WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, active,
	).Scan(&sendID); err != nil {
		t.Fatalf("find materialized row: %v", err)
	}

	// Directly diverge the snapshot from the live subscriber row.
	// Unreachable in production today (no email-change flow) — this test
	// pins the invariant, not a bug.
	if _, err := pool.Exec(ctx,
		`UPDATE email_sends SET email = 'zz-mailing-diverged@example.com' WHERE id = $1`, sendID,
	); err != nil {
		t.Fatalf("diverge snapshot email: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	recipients, skipped, err := store.RecheckEligibleTx(ctx, tx, campaignID, []int64{sendID})
	if err != nil {
		t.Fatalf("RecheckEligibleTx: %v", err)
	}
	if len(recipients) != 0 {
		t.Errorf("recipients = %v, want empty (snapshot email diverged from live)", recipients)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %+v, want exactly one", skipped)
	}
}

// ── HTTP handler-facing seam: LoadAudience/Preview error mapping ───────────

func TestAudienceStore_LoadAudience_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewAudienceStore(pool)
	ctx := context.Background()

	store2 := NewCampaignStore(pool)
	c, err := store2.Create(ctx, CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	campaignID := c.ID
	// Delete immediately rather than via cleanupCampaign/t.Cleanup (which
	// only runs at test end) — this assertion needs a genuinely-missing id
	// right now.
	if _, err := pool.Exec(ctx, `DELETE FROM email_campaigns WHERE id = $1`, campaignID); err != nil {
		t.Fatalf("delete campaign: %v", err)
	}

	if _, err := store.LoadAudience(ctx, campaignID); !errors.Is(err, ErrCampaignNotFound) {
		t.Errorf("LoadAudience error = %v, want ErrCampaignNotFound", err)
	}
}

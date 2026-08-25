package outbox

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func uniqueRecipient(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("outbox-test-%d@example.com", testdb.Unique())
}

// TestOutbox_EnqueueTx_RollbackLeavesNoRow is the load-bearing property
// #0126 exists to establish: a transaction rolled back after EnqueueTx
// leaves no outbound_queue row at all. Enqueueing after the commit (rather
// than inside it) would reintroduce the exact gap this issue closes with a
// smaller window instead of none.
func TestOutbox_EnqueueTx_RollbackLeavesNoRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	recipient := uniqueRecipient(t)
	id, err := store.EnqueueTx(ctx, tx, Item{Kind: KindConfirmation, Recipient: recipient})
	if err != nil {
		t.Fatalf("EnqueueTx: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected a non-zero id")
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_queue WHERE recipient = $1`, recipient).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatalf("rollback left %d row(s) behind, want 0", count)
	}
}

// TestOutbox_EnqueueTx_CommitPersistsRow is the positive twin: a committed
// transaction's enqueue is visible afterward.
func TestOutbox_EnqueueTx_CommitPersistsRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	recipient := uniqueRecipient(t)
	id, err := store.EnqueueTx(ctx, tx, Item{Kind: KindConfirmation, Recipient: recipient})
	if err != nil {
		t.Fatalf("EnqueueTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("select after commit: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("status = %q, want %q", status, StatusQueued)
	}
}

// TestOutbox_ClaimDue_OnlyClaimsQueuedAndDue verifies ClaimDue's WHERE
// clause: a row not yet due, and a row already claimed by someone else,
// are both left alone.
func TestOutbox_ClaimDue_OnlyClaimsQueuedAndDue(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	due := uniqueRecipient(t)
	notDue := uniqueRecipient(t)

	dueID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: due})
	if err != nil {
		t.Fatalf("Enqueue due: %v", err)
	}
	if _, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: notDue}); err != nil {
		t.Fatalf("Enqueue notDue: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() + interval '1 hour' WHERE recipient = $1`, notDue); err != nil {
		t.Fatalf("pushing notDue into the future: %v", err)
	}

	rows, err := store.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	var gotDue bool
	for _, r := range rows {
		if r.Recipient == notDue {
			t.Fatalf("ClaimDue claimed a not-yet-due row")
		}
		if r.Recipient == due {
			gotDue = true
			if r.ID != dueID {
				t.Fatalf("claimed row id = %d, want %d", r.ID, dueID)
			}
			if r.Attempts != 1 {
				t.Fatalf("Attempts = %d, want 1 (claim increments)", r.Attempts)
			}
			if r.Status != StatusSending {
				t.Fatalf("Status = %q, want %q", r.Status, StatusSending)
			}
			if r.ClaimedAt == nil {
				t.Fatalf("ClaimedAt not stamped")
			}
		}
	}
	if !gotDue {
		t.Fatalf("ClaimDue did not claim the due row")
	}

	// A second claim must not re-claim the row that is now 'sending'.
	rows2, err := store.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("second ClaimDue: %v", err)
	}
	for _, r := range rows2 {
		if r.ID == dueID {
			t.Fatalf("second ClaimDue re-claimed an already-sending row")
		}
	}
}

func TestOutbox_MarkSent_ScrubsPayload(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	id, err := store.Enqueue(ctx, Item{
		Kind:      KindConfirmation,
		Recipient: uniqueRecipient(t),
		Payload:   map[string]any{"confirm_token": "secret-token-value"},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	var claimed *Row
	for i := range rows {
		if rows[i].ID == id {
			claimed = &rows[i]
		}
	}
	if claimed == nil {
		t.Fatalf("row %d was not claimed", id)
	}

	done, err := store.MarkSent(ctx, id, "ses-message-id-123")
	if err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	if !done {
		t.Fatalf("MarkSent reported done=false")
	}

	var status, payload string
	var sesID *string
	var sentAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, payload::text, ses_message_id, sent_at FROM outbound_queue WHERE id = $1`, id,
	).Scan(&status, &payload, &sesID, &sentAt); err != nil {
		t.Fatalf("select after MarkSent: %v", err)
	}
	if status != StatusSent {
		t.Fatalf("status = %q, want %q", status, StatusSent)
	}
	if payload != "{}" {
		t.Fatalf("payload = %q, want scrubbed to {}", payload)
	}
	if sesID == nil || *sesID != "ses-message-id-123" {
		t.Fatalf("ses_message_id = %v, want ses-message-id-123", sesID)
	}
	if sentAt == nil {
		t.Fatalf("sent_at not stamped")
	}

	// A second MarkSent on an already-sent row must not report done.
	done2, err := store.MarkSent(ctx, id, "ignored")
	if err != nil {
		t.Fatalf("second MarkSent: %v", err)
	}
	if done2 {
		t.Fatalf("second MarkSent on an already-sent row reported done=true")
	}
}

func TestOutbox_BackoffSchedule(t *testing.T) {
	want := map[int]time.Duration{
		1: time.Minute,
		2: 5 * time.Minute,
		3: 15 * time.Minute,
		4: time.Hour,
		5: 6 * time.Hour,
		6: 24 * time.Hour,
		7: 24 * time.Hour, // clamp — PRD §6.11 leaves 7/8 undefined; #0126's plan clamps to the last step
		8: 24 * time.Hour,
	}
	for attempts, want := range want {
		if got := Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestOutbox_AbandonsAtMaxRetries_RetainsLastError(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	id, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("forcing due: %v", err)
		}
		rows, err := store.ClaimDue(ctx, 10)
		if err != nil {
			t.Fatalf("ClaimDue attempt %d: %v", attempt, err)
		}
		if len(rows) != 1 || rows[0].ID != id {
			t.Fatalf("attempt %d: expected to claim row %d, got %+v", attempt, id, rows)
		}
		if rows[0].Attempts != attempt {
			t.Fatalf("attempt %d: Attempts = %d", attempt, rows[0].Attempts)
		}

		errMsg := fmt.Sprintf("send failed on attempt %d", attempt)
		done, err := store.MarkRetryOrAbandon(ctx, id, rows[0].Attempts, errMsg, maxRetries)
		if err != nil {
			t.Fatalf("MarkRetryOrAbandon attempt %d: %v", attempt, err)
		}
		if !done {
			t.Fatalf("MarkRetryOrAbandon attempt %d reported done=false", attempt)
		}
	}

	var status string
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT status, error FROM outbound_queue WHERE id = $1`, id).Scan(&status, &lastErr); err != nil {
		t.Fatalf("select after abandon: %v", err)
	}
	if status != StatusAbandoned {
		t.Fatalf("status = %q, want %q", status, StatusAbandoned)
	}
	if lastErr == nil || *lastErr != "send failed on attempt 3" {
		t.Fatalf("error = %v, want the last attempt's message retained", lastErr)
	}

	// Abandoned rows must never be reclaimed by ClaimDue.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("forcing due after abandon: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDue after abandon: %v", err)
	}
	for _, r := range rows {
		if r.ID == id {
			t.Fatalf("ClaimDue reclaimed an abandoned row")
		}
	}
}

func TestOutbox_MarkRetryOrAbandon_RetriesBeforeMax(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	id, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected to claim 1 row, got %d", len(rows))
	}

	done, err := store.MarkRetryOrAbandon(ctx, id, rows[0].Attempts, "transient failure", 8)
	if err != nil {
		t.Fatalf("MarkRetryOrAbandon: %v", err)
	}
	if !done {
		t.Fatalf("MarkRetryOrAbandon reported done=false")
	}

	var status string
	var claimedAt *time.Time
	var nextAttemptAt time.Time
	if err := pool.QueryRow(ctx, `SELECT status, claimed_at, next_attempt_at FROM outbound_queue WHERE id = $1`, id).
		Scan(&status, &claimedAt, &nextAttemptAt); err != nil {
		t.Fatalf("select: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("status = %q, want %q", status, StatusQueued)
	}
	if claimedAt != nil {
		t.Fatalf("claimed_at not cleared")
	}
	if !nextAttemptAt.After(time.Now()) {
		t.Fatalf("next_attempt_at = %v, want it pushed into the future", nextAttemptAt)
	}
}

func TestOutbox_Release_RequeuesImmediately(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	id, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.ClaimDue(ctx, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}

	done, err := store.Release(ctx, id)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !done {
		t.Fatalf("Release reported done=false")
	}

	var status string
	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, claimed_at FROM outbound_queue WHERE id = $1`, id).Scan(&status, &claimedAt); err != nil {
		t.Fatalf("select: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("status = %q, want %q", status, StatusQueued)
	}
	if claimedAt != nil {
		t.Fatalf("claimed_at not cleared")
	}

	// A released row is due again immediately.
	rows, err := store.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDue after release: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("released row was not immediately re-claimable")
	}
}

// TestOutbox_OrphanSweep_DoesNotUnclaimLiveRow is the #0122 shape (see that
// issue and internal/mailing's own
// TestWorker_TwoWorkersOneCampaign_OrphanSweepDuringLiveClaim_NoDuplicate):
// a row claimed moments ago by a still-alive worker must not be swept, only
// one whose claim predates staleAfter.
func TestOutbox_OrphanSweep_DoesNotUnclaimLiveRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	liveID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue live: %v", err)
	}
	staleID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue stale: %v", err)
	}

	if _, err := store.ClaimDue(ctx, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	// Backdate only the "stale" row's claim.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET claimed_at = now() - interval '1 hour' WHERE id = $1`, staleID); err != nil {
		t.Fatalf("backdating claimed_at: %v", err)
	}

	swept, err := store.OrphanSweep(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("OrphanSweep: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1", swept)
	}

	var liveStatus, staleStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, liveID).Scan(&liveStatus); err != nil {
		t.Fatalf("select live: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, staleID).Scan(&staleStatus); err != nil {
		t.Fatalf("select stale: %v", err)
	}
	if liveStatus != StatusSending {
		t.Fatalf("live row status = %q, want %q (must not be un-claimed)", liveStatus, StatusSending)
	}
	if staleStatus != StatusQueued {
		t.Fatalf("stale row status = %q, want %q", staleStatus, StatusQueued)
	}
}

func TestOutbox_Counts(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	queuedID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue queued: %v", err)
	}
	sentID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue sent: %v", err)
	}
	if _, err := store.ClaimDue(ctx, 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if _, err := store.MarkSent(ctx, sentID, "msg-id"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	counts, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Queued < 1 {
		t.Fatalf("Queued = %d, want at least 1 (id %d)", counts.Queued, queuedID)
	}
	if counts.Sent < 1 {
		t.Fatalf("Sent = %d, want at least 1", counts.Sent)
	}
}

// TestOutbox_LatestByRecipients_ReturnsMostRecentPerRecipient is #0128's
// proof: for a recipient with more than one outbound_queue row of the same
// kind, LatestByRecipients returns the MOST RECENT one (by created_at), not
// an arbitrary one — the pending-subscriber screen's "outbound queue state
// for each pending address" depends on this being the row that actually
// reflects the last attempt, e.g. after an admin resend superseded an
// earlier abandoned row.
func TestOutbox_LatestByRecipients_ReturnsMostRecentPerRecipient(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	recipient := uniqueRecipient(t)
	olderID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: recipient})
	if err != nil {
		t.Fatalf("Enqueue older: %v", err)
	}
	// Abandon the older row so it is clearly distinguishable from the newer one.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET status = $2, created_at = created_at - interval '1 hour' WHERE id = $1`, olderID, StatusAbandoned); err != nil {
		t.Fatalf("backdating/abandoning older row: %v", err)
	}
	newerID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: recipient})
	if err != nil {
		t.Fatalf("Enqueue newer: %v", err)
	}

	byRecipient, err := store.LatestByRecipients(ctx, KindConfirmation, []string{recipient})
	if err != nil {
		t.Fatalf("LatestByRecipients: %v", err)
	}
	row, ok := byRecipient[recipient]
	if !ok {
		t.Fatalf("no row returned for recipient %q", recipient)
	}
	if row.ID != newerID {
		t.Errorf("LatestByRecipients returned row %d (status %q), want the newer row %d (older was %d, abandoned)", row.ID, row.Status, newerID, olderID)
	}
	if row.Status != StatusQueued {
		t.Errorf("returned row status = %q, want %q (the newer, still-queued row)", row.Status, StatusQueued)
	}
}

// TestOutbox_LatestByRecipients_AbsentRecipientOmittedNotErrored proves a
// recipient with no outbound_queue row of the requested kind is simply
// absent from the returned map, not an error and not a zero-value entry —
// the handler layer renders that as "never queued" (#0128).
func TestOutbox_LatestByRecipients_AbsentRecipientOmittedNotErrored(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	neverQueued := uniqueRecipient(t)
	byRecipient, err := store.LatestByRecipients(ctx, KindConfirmation, []string{neverQueued})
	if err != nil {
		t.Fatalf("LatestByRecipients: %v", err)
	}
	if _, ok := byRecipient[neverQueued]; ok {
		t.Errorf("byRecipient unexpectedly contains an entry for a recipient with no rows")
	}
}

// TestOutbox_LatestByRecipients_ScopedToKind proves a row of a DIFFERENT
// kind for the same recipient does not leak into a query for another kind
// — resend's confirmation-kind lookup must not surface, say, a welcome-kind
// row for the same address.
func TestOutbox_LatestByRecipients_ScopedToKind(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	recipient := uniqueRecipient(t)
	if _, err := store.Enqueue(ctx, Item{Kind: KindWelcome, Recipient: recipient}); err != nil {
		t.Fatalf("Enqueue welcome: %v", err)
	}

	byRecipient, err := store.LatestByRecipients(ctx, KindConfirmation, []string{recipient})
	if err != nil {
		t.Fatalf("LatestByRecipients: %v", err)
	}
	if _, ok := byRecipient[recipient]; ok {
		t.Errorf("byRecipient unexpectedly contains a kind=welcome row when querying kind=confirmation")
	}
}

// TestOutbox_LatestByRecipients_EmptyRecipientsReturnsEmptyMapWithoutQuery
// proves the empty-slice short circuit both empties correctly and is safe
// to call (no SQL error from an empty ANY($2) array edge case some
// Postgres drivers mishandle).
func TestOutbox_LatestByRecipients_EmptyRecipientsReturnsEmptyMapWithoutQuery(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	byRecipient, err := store.LatestByRecipients(ctx, KindConfirmation, nil)
	if err != nil {
		t.Fatalf("LatestByRecipients: %v", err)
	}
	if len(byRecipient) != 0 {
		t.Errorf("byRecipient = %v, want empty for a nil recipients slice", byRecipient)
	}
}

// TestOutbox_AbandonedCountByKind_ScopedToKind proves the count is scoped
// to the requested kind, not an account-wide abandoned total (#0128 — the
// admin overview's "confirmations abandoned" figure must not move when a
// DIFFERENT kind abandons).
func TestOutbox_AbandonedCountByKind_ScopedToKind(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	before, err := store.AbandonedCountByKind(ctx, KindConfirmation)
	if err != nil {
		t.Fatalf("AbandonedCountByKind before: %v", err)
	}

	// Abandon a REGISTRATION row — must not move the confirmation count.
	regID, err := store.Enqueue(ctx, Item{Kind: KindRegistration, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue registration: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET status = $2 WHERE id = $1`, regID, StatusAbandoned); err != nil {
		t.Fatalf("abandoning registration row: %v", err)
	}
	afterRegistration, err := store.AbandonedCountByKind(ctx, KindConfirmation)
	if err != nil {
		t.Fatalf("AbandonedCountByKind after registration abandon: %v", err)
	}
	if afterRegistration != before {
		t.Errorf("AbandonedCountByKind(confirmation) moved from %d to %d after abandoning a REGISTRATION row", before, afterRegistration)
	}

	// Now abandon a CONFIRMATION row — must move the count by exactly 1.
	confirmID, err := store.Enqueue(ctx, Item{Kind: KindConfirmation, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue confirmation: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET status = $2 WHERE id = $1`, confirmID, StatusAbandoned); err != nil {
		t.Fatalf("abandoning confirmation row: %v", err)
	}
	afterConfirmation, err := store.AbandonedCountByKind(ctx, KindConfirmation)
	if err != nil {
		t.Fatalf("AbandonedCountByKind after confirmation abandon: %v", err)
	}
	if afterConfirmation != before+1 {
		t.Errorf("AbandonedCountByKind(confirmation) = %d after abandoning one confirmation row, want %d", afterConfirmation, before+1)
	}
}

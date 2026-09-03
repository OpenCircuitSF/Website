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

// distinctKind returns a Kind unique to this test run (#0285).
//
// This is NOT defence against internal/mailing or internal/handlers running
// concurrently with this package's tests — they cannot. Every DB-backed
// package's TestMain calls internal/testdb.Lock() on the same fixed
// advisory-lock key, and that package's own doc comment says plainly that
// "only one package can hold the lock at a time, so they run one-at-a-time
// even under `go test ./...`" — measured directly during #0285's review by
// launching two `go test` binaries against one database and polling
// pg_locks: one holder, one blocked waiter, never overlapping. This
// package's own main_test.go also TRUNCATEs outbound_queue on entry while
// holding that lock, so even sequential residue from an earlier package's
// run is gone before this package's first test executes.
//
// The real reason to scope by a distinct kind: it makes each assertion
// about rows the test itself created, independent of anything else that
// might be in the table — a future test added to THIS package, a future run
// that stops truncating on entry, or any other change to the locking or
// truncation model above. outbound_queue.kind carries no CHECK constraint
// (see the package doc comment), so a value no production code ever
// enqueues is exactly as valid a row as any real Kind, and nothing but this
// test's own Enqueue calls can ever produce one. internal/mailing and
// internal/handlers do enqueue real rows — including outbox.KindConfirmation
// and outbox.KindSubscribeIntake — into this same outbound_queue table from
// their own DB-backed tests (confirmed by grep), which is exactly why a
// test in THIS package must never claim or count against those real Kind
// values: not because those other packages' tests could be running at the
// same moment, but because a real Kind is shared surface this package does
// not own, and scoping to it alone is not scoping at all.
func distinctKind(t *testing.T) Kind {
	t.Helper()
	return Kind(fmt.Sprintf("test-outbox-%d", testdb.Unique()))
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

	rows, err := store.ClaimDue(ctx, 10, AllKinds)
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
	rows2, err := store.ClaimDue(ctx, 10, AllKinds)
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
	rows, err := store.ClaimDue(ctx, 10, AllKinds)
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

	// #0285: scoped to a kind unique to this test so the ClaimDue calls
	// below — which assert an exact claimed count — cannot observe a row
	// enqueued under a real, shared Kind by this package's own future
	// tests or by another package's tests against this same
	// outbound_queue table (see distinctKind's doc comment).
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1`, id); err != nil {
			t.Fatalf("forcing due: %v", err)
		}
		rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
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
	rows, err := store.ClaimDue(ctx, 10, AllKinds)
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

	// #0285: scoped to a kind unique to this test; see distinctKind's
	// doc comment for why a real, shared Kind is surface this package
	// does not own and an unscoped ClaimDue here should not claim
	// against it.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
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
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT status, claimed_at, next_attempt_at, error FROM outbound_queue WHERE id = $1`, id).
		Scan(&status, &claimedAt, &nextAttemptAt, &lastErr); err != nil {
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
	// #0272 criterion 3: a queued row awaiting retry keeps the error from
	// its most recent failed attempt — that is the case the column exists
	// for, and only 'sent' (MarkSent) clears it.
	if lastErr == nil || *lastErr != "transient failure" {
		t.Fatalf("error = %v, want the retry's error retained while queued", lastErr)
	}
}

// TestOutbox_MarkSent_ClearsErrorFromFailedAttempt is #0272: a row that
// failed once (error populated, still 'queued' for retry) and then
// succeeds on a later attempt must read as a clean success, not a row that
// LOOKS like a failure because status='sent' sits next to a stale error
// from the attempt before it. MarkSent must clear error in the same UPDATE
// that sets status='sent', ses_message_id, and sent_at.
func TestOutbox_MarkSent_ClearsErrorFromFailedAttempt(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// #0285: scoped to a kind unique to this test; see distinctKind's
	// doc comment for why a real, shared Kind is surface this package
	// does not own and an unscoped ClaimDue here should not claim
	// against it.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Attempt 1: claim and fail, leaving error populated on a 'queued' row.
	rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
	if err != nil {
		t.Fatalf("ClaimDue (attempt 1): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("expected to claim row %d, got %+v", id, rows)
	}
	if _, err := store.MarkRetryOrAbandon(ctx, id, rows[0].Attempts, "transient send failure", 8); err != nil {
		t.Fatalf("MarkRetryOrAbandon: %v", err)
	}

	var errAfterFail *string
	if err := pool.QueryRow(ctx, `SELECT error FROM outbound_queue WHERE id = $1`, id).Scan(&errAfterFail); err != nil {
		t.Fatalf("select after failed attempt: %v", err)
	}
	if errAfterFail == nil || *errAfterFail != "transient send failure" {
		t.Fatalf("error after failed attempt = %v, want it populated", errAfterFail)
	}

	// Attempt 2: force due, claim again, and succeed.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("forcing due: %v", err)
	}
	rows2, err := store.ClaimDue(ctx, 10, []Kind{kind})
	if err != nil {
		t.Fatalf("ClaimDue (attempt 2): %v", err)
	}
	if len(rows2) != 1 || rows2[0].ID != id {
		t.Fatalf("expected to reclaim row %d, got %+v", id, rows2)
	}

	done, err := store.MarkSent(ctx, id, "ses-message-id-after-retry")
	if err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	if !done {
		t.Fatalf("MarkSent reported done=false")
	}

	var status string
	var lastErr *string
	var sesID *string
	var sentAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, error, ses_message_id, sent_at FROM outbound_queue WHERE id = $1`, id,
	).Scan(&status, &lastErr, &sesID, &sentAt); err != nil {
		t.Fatalf("select after MarkSent: %v", err)
	}
	if status != StatusSent {
		t.Fatalf("status = %q, want %q", status, StatusSent)
	}
	if lastErr != nil {
		t.Fatalf("error = %v, want NULL — a sent row must not carry a stale error from a prior attempt", *lastErr)
	}
	if sesID == nil || *sesID != "ses-message-id-after-retry" {
		t.Fatalf("ses_message_id = %v, want ses-message-id-after-retry", sesID)
	}
	if sentAt == nil {
		t.Fatalf("sent_at not stamped")
	}
}

// TestOutbox_MarkDone_ClearsErrorFromSupersededAttempt is #0288: it drives
// the real convergence subscribe.go's own comment anticipates — the
// recovery poller claims an intake row and fails it via
// MarkRetryOrAbandon (writing error, putting the row back to 'queued'),
// and the request's own fast path then MarkDones that same row. The row
// must land 'sent' with error cleared, not carrying the error from the
// attempt that was subsequently superseded.
func TestOutbox_MarkDone_ClearsErrorFromSupersededAttempt(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// #0285: a distinct kind, not the real KindSubscribeIntake — Store's
	// ClaimDue/MarkRetryOrAbandon/MarkDone never branch on Kind, so this
	// loses no coverage while making the exact claimed-row count below
	// immune to real KindSubscribeIntake rows internal/handlers' own
	// tests enqueue into this same shared table (see distinctKind's doc
	// comment).
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// The recovery poller claims the row and fails it, leaving it 'queued'
	// with error populated.
	rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("expected to claim row %d, got %+v", id, rows)
	}
	if _, err := store.MarkRetryOrAbandon(ctx, id, rows[0].Attempts, "recovery poller: transient failure", 8); err != nil {
		t.Fatalf("MarkRetryOrAbandon: %v", err)
	}

	var statusAfterFail string
	var errAfterFail *string
	if err := pool.QueryRow(ctx, `SELECT status, error FROM outbound_queue WHERE id = $1`, id).Scan(&statusAfterFail, &errAfterFail); err != nil {
		t.Fatalf("select after failed attempt: %v", err)
	}
	if statusAfterFail != StatusQueued {
		t.Fatalf("status after failed attempt = %q, want %q", statusAfterFail, StatusQueued)
	}
	if errAfterFail == nil || *errAfterFail != "recovery poller: transient failure" {
		t.Fatalf("error after failed attempt = %v, want it populated", errAfterFail)
	}

	// The request's fast path converges on the same row and marks it done.
	done, err := store.MarkDone(ctx, id)
	if err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if !done {
		t.Fatalf("MarkDone reported done=false")
	}

	var status string
	var lastErr *string
	var sentAt *time.Time
	var payload string
	if err := pool.QueryRow(ctx,
		`SELECT status, error, sent_at, payload::text FROM outbound_queue WHERE id = $1`, id,
	).Scan(&status, &lastErr, &sentAt, &payload); err != nil {
		t.Fatalf("select after MarkDone: %v", err)
	}
	if status != StatusSent {
		t.Fatalf("status = %q, want %q", status, StatusSent)
	}
	if lastErr != nil {
		t.Fatalf("error = %v, want NULL — a done row must not carry a stale error from a superseded attempt", *lastErr)
	}
	if sentAt == nil {
		t.Fatalf("sent_at not stamped")
	}
	if payload != "{}" {
		t.Fatalf("payload = %q, want scrubbed to {}", payload)
	}
}

// TestOutbox_MarkDone_ClearsErrorFromSendingState covers MarkDone's other
// admitted source state directly (#0288 criterion 2): a row still
// 'sending' — the recovery poller claimed it but had not yet failed or
// completed it — must also clear on MarkDone, not just a row that
// round-tripped through 'queued'.
func TestOutbox_MarkDone_ClearsErrorFromSendingState(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// #0285 review, finding 2: a distinct kind, not the real
	// KindSubscribeIntake — Store's ClaimDue/MarkDone never branch on Kind,
	// so this loses no coverage. An unscoped-by-id ClaimDue(ctx, 10,
	// KindSubscribeIntake) over the real Kind can claim another test's
	// deliberately-seeded due intake row out from under it (see
	// distinctKind's doc comment on why a real Kind is shared surface this
	// package does not own) — exactly the sibling shape
	// TestOutbox_MarkDone_ClearsErrorFromSupersededAttempt's own comment,
	// three functions earlier, already warns against.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.ClaimDue(ctx, 10, []Kind{kind}); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	// Simulate a stray error already sitting on a 'sending' row (defensive
	// — nothing in this codebase writes error without also changing
	// status, but MarkDone's SET clause must clear it regardless of how it
	// got there).
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET error = $2 WHERE id = $1`, id, "stray error"); err != nil {
		t.Fatalf("seeding stray error: %v", err)
	}

	done, err := store.MarkDone(ctx, id)
	if err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if !done {
		t.Fatalf("MarkDone reported done=false")
	}

	var status string
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT status, error FROM outbound_queue WHERE id = $1`, id).Scan(&status, &lastErr); err != nil {
		t.Fatalf("select after MarkDone: %v", err)
	}
	if status != StatusSent {
		t.Fatalf("status = %q, want %q", status, StatusSent)
	}
	if lastErr != nil {
		t.Fatalf("error = %v, want NULL after MarkDone from 'sending'", *lastErr)
	}
}

// TestOutbox_MarkSent_RecordsSendAfterClaimReleasedMidSend is #0283's
// criterion 4: it drives the actual scenario the issue describes — claim a
// row, release the claim underneath the sender (simulating a same-kind
// OrphanSweep or similar racing a still-live claim), complete the send, and
// assert the row is recorded 'sent' rather than left 'queued' to be sent
// again by a later pass.
func TestOutbox_MarkSent_RecordsSendAfterClaimReleasedMidSend(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// #0285: scoped to a kind unique to this test; see distinctKind's
	// doc comment for why a real, shared Kind is surface this package
	// does not own and an unscoped ClaimDue here should not claim
	// against it.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("expected to claim row %d, got %+v", id, rows)
	}

	// The claim is released back to 'queued' while the (simulated) send is
	// still in flight — the exact race #0283 describes.
	released, err := store.Release(ctx, id)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !released {
		t.Fatalf("Release reported done=false")
	}
	var statusAfterRelease string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&statusAfterRelease); err != nil {
		t.Fatalf("select after release: %v", err)
	}
	if statusAfterRelease != StatusQueued {
		t.Fatalf("status after release = %q, want %q", statusAfterRelease, StatusQueued)
	}

	// The send that was already in flight now completes — SES already has
	// the message by this point in the real scenario.
	done, err := store.MarkSent(ctx, id, "ses-message-id-after-release")
	if err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	if !done {
		t.Fatalf("MarkSent reported done=false — the send would be silently lost and the row re-sent later")
	}

	var status string
	var sesID *string
	var sentAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, ses_message_id, sent_at FROM outbound_queue WHERE id = $1`, id,
	).Scan(&status, &sesID, &sentAt); err != nil {
		t.Fatalf("select after MarkSent: %v", err)
	}
	if status != StatusSent {
		t.Fatalf("status = %q, want %q", status, StatusSent)
	}
	if sesID == nil || *sesID != "ses-message-id-after-release" {
		t.Fatalf("ses_message_id = %v, want ses-message-id-after-release", sesID)
	}
	if sentAt == nil {
		t.Fatalf("sent_at not stamped")
	}

	// Not sent twice: a later pass must not be able to reclaim and resend
	// the row now that it is recorded 'sent'.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET next_attempt_at = now() WHERE id = $1`, id); err != nil {
		t.Fatalf("forcing due: %v", err)
	}
	rows2, err := store.ClaimDue(ctx, 10, AllKinds)
	if err != nil {
		t.Fatalf("ClaimDue after MarkSent: %v", err)
	}
	for _, r := range rows2 {
		if r.ID == id {
			t.Fatalf("a sent row was reclaimed — the row would be sent twice")
		}
	}
}

// TestOutbox_MarkSent_DoesNotResurrectAbandonedRow is #0283 criterion 2: a
// row that already reached the terminal 'abandoned' state must not
// silently become 'sent'. MarkSent's widened predicate deliberately
// excludes 'abandoned' — this proves it stays excluded and the row's
// diagnostic state (status, error) survives untouched.
func TestOutbox_MarkSent_DoesNotResurrectAbandonedRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// #0285: scoped to a kind unique to this test; see distinctKind's
	// doc comment for why a real, shared Kind is surface this package
	// does not own and an unscoped ClaimDue here should not claim
	// against it.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("expected to claim row %d, got %+v", id, rows)
	}
	// Abandon immediately (maxRetries=1 so attempts=1 hits the abandon
	// branch) rather than looping through the full schedule.
	if _, err := store.MarkRetryOrAbandon(ctx, id, rows[0].Attempts, "exhausted retries", 1); err != nil {
		t.Fatalf("MarkRetryOrAbandon: %v", err)
	}
	var statusAfterAbandon string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&statusAfterAbandon); err != nil {
		t.Fatalf("select after abandon: %v", err)
	}
	if statusAfterAbandon != StatusAbandoned {
		t.Fatalf("status after abandon = %q, want %q", statusAfterAbandon, StatusAbandoned)
	}

	// A delayed MarkSent for the same id (e.g. a very late-arriving call
	// from the attempt that led to abandonment) must not resurrect it.
	done, err := store.MarkSent(ctx, id, "should-not-be-recorded")
	if err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	if done {
		t.Fatalf("MarkSent reported done=true against an abandoned row — it must not resurrect it")
	}

	var status string
	var lastErr *string
	var sesID *string
	if err := pool.QueryRow(ctx,
		`SELECT status, error, ses_message_id FROM outbound_queue WHERE id = $1`, id,
	).Scan(&status, &lastErr, &sesID); err != nil {
		t.Fatalf("select after MarkSent attempt: %v", err)
	}
	if status != StatusAbandoned {
		t.Fatalf("status = %q, want %q (unchanged)", status, StatusAbandoned)
	}
	if lastErr == nil || *lastErr != "exhausted retries" {
		t.Fatalf("error = %v, want the abandon reason retained", lastErr)
	}
	if sesID != nil {
		t.Fatalf("ses_message_id = %v, want NULL — MarkSent must not have written anything", *sesID)
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
	if _, err := store.ClaimDue(ctx, 10, AllKinds); err != nil {
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
	rows, err := store.ClaimDue(ctx, 10, AllKinds)
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
//
// #0285: the sweep and its exact swept-count assertion are scoped to a
// kind unique to this test (see distinctKind's doc comment). An earlier
// version of this test asserted swept==1 from an UNSCOPED sweep, which
// counted every stale 'sending' row in the whole table — a real Kind, or a
// future test in this same package, is shared surface this test does not
// own. No concrete failing interleaving of the old unscoped assertion has
// been demonstrated, and internal/testdb's locking model rules out the
// cross-package one an earlier draft of this comment claimed (see
// distinctKind's doc comment). This scoping is defence in depth against
// that bad assertion shape, not a fix for an observed flake.
// TestOutbox_OrphanSweep_Unscoped_SweepsAcrossKinds below keeps the
// unscoped code path covered without depending on an exact total.
func TestOutbox_OrphanSweep_DoesNotUnclaimLiveRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	kind := distinctKind(t)
	liveID, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue live: %v", err)
	}
	staleID, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue stale: %v", err)
	}

	if _, err := store.ClaimDue(ctx, 10, []Kind{kind}); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	// Backdate only the "stale" row's claim.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET claimed_at = now() - interval '1 hour' WHERE id = $1`, staleID); err != nil {
		t.Fatalf("backdating claimed_at: %v", err)
	}

	swept, err := store.OrphanSweep(ctx, 5*time.Minute, []Kind{kind})
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

// TestOutbox_OrphanSweep_Unscoped_SweepsAcrossKinds keeps OrphanSweep's
// unscoped default (no kinds argument — "sweep across every kind",
// documented on OrphanSweep and exercised by every caller before #0254)
// covered, per #0285 criterion 3: the fix for the flaky exact-count
// assertion above is to scope it, not to delete coverage of the unscoped
// path, since it remains documented behaviour that #0281 may formalise.
//
// This does NOT assert an exact swept count — an unscoped sweep by
// definition affects every stale 'sending' row in the table, not just
// this test's own two, so an exact total is exactly the assertion #0285
// removed above. Instead it proves the two properties that matter and
// belong to this test's own rows: its stale row is included in an
// unscoped sweep (swept >= 1 is too weak on its own — some other stale
// row already in the table could coincidentally produce a nonzero count
// without ever having touched this row — so the row's own resulting
// status is checked directly), and its live row is not.
func TestOutbox_OrphanSweep_Unscoped_SweepsAcrossKinds(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	kind := distinctKind(t)
	liveID, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue live: %v", err)
	}
	staleID, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue stale: %v", err)
	}

	if _, err := store.ClaimDue(ctx, 10, []Kind{kind}); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET claimed_at = now() - interval '1 hour' WHERE id = $1`, staleID); err != nil {
		t.Fatalf("backdating claimed_at: %v", err)
	}

	if _, err := store.OrphanSweep(ctx, 5*time.Minute, AllKinds); err != nil {
		t.Fatalf("OrphanSweep (unscoped): %v", err)
	}

	var liveStatus, staleStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, liveID).Scan(&liveStatus); err != nil {
		t.Fatalf("select live: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, staleID).Scan(&staleStatus); err != nil {
		t.Fatalf("select stale: %v", err)
	}
	if liveStatus != StatusSending {
		t.Fatalf("live row status = %q, want %q (an unscoped sweep must still respect staleness)", liveStatus, StatusSending)
	}
	if staleStatus != StatusQueued {
		t.Fatalf("stale row status = %q, want %q (an unscoped sweep must still catch a stale row of any kind)", staleStatus, StatusQueued)
	}
}

// TestOutbox_Counts proves Counts partitions rows by status correctly: a
// row that stays queued is counted under Queued, and a row that is claimed
// and marked sent is counted under Sent. Measured as a delta around this
// test's own two rows, each scoped to its own kind unique to this test run
// (distinctKind's doc comment, #0285), so the assertion is about rows this
// test itself created rather than whatever an earlier test in this package
// happened to leave behind.
//
// #0386 -- the previous version enqueued both rows under the same real
// Kind, then called ClaimDue(ctx, 10, AllKinds), which claims every due row
// up to its limit -- both of them, not just the one this test went on to
// mark sent. That left nothing queued from this test's own rows, so
// `counts.Queued >= 1` only ever passed because an EARLIER test in this
// package leaves queued rows behind: run alone
// (`go test ./internal/outbox/ -run '^TestOutbox_Counts$'`) it failed with
// `Queued = 0, want at least 1`. This version claims only the row bound for
// Sent -- scoped to ITS OWN distinct kind, so ClaimDue cannot touch the row
// left queued -- and asserts a delta on each side rather than an absolute
// floor, so it needs no residue from any other test to pass.
//
// #0389 -- the two after-deltas above (Queued +1, Sent +1) are individually
// sound but jointly unable to tell the two labels apart: a Counts that
// swaps which SQL argument position feeds Queued and which feeds Sent
// reports Sent's real count under the name Queued and vice versa, and both
// deltas still land on +1, so the swap leaves this whole package green
// (measured by #0382/#0386's reviewer). The mid-point assertion below,
// taken after both rows are enqueued but BEFORE either is claimed or sent,
// is asymmetric on purpose: at that instant this test has added two rows to
// Queued and zero to Sent, so a swapped Counts reports mid.Queued as the
// unchanged real Sent count rather than before.Queued+2 -- no relabelling
// can satisfy an assertion built around a delta the mutation itself cannot
// produce under either label.
//
// #0390 -- the same gap existed between Sending and Abandoned: nothing in
// this package asserted Sending at all, and Abandoned was only ever
// asserted unchanged (in TestOutbox_Counts_ReportsSkippedDistinctlyFromAbandoned),
// so a Counts that swaps which SQL argument feeds Sending and which feeds
// Abandoned left the whole package green too. A third row, claimed and
// deliberately left mid-flight -- never marked sent or abandoned -- moves
// the real Sending count +1 and leaves the real Abandoned count unchanged.
// That row is claimed and measured (midSending, below) BEFORE sentKind's
// own row is claimed and marked sent: taking the snapshot afterward instead
// made Sending's and Sent's deltas coincide at +1 apiece by the test's end,
// so a Sending<->Sent swap also went undetected (measured directly). Taken
// before sentID is touched, the two deltas differ -- Sending +1, Sent +0 --
// which is what actually discriminates the two labels.
func TestOutbox_Counts(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	before, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (before): %v", err)
	}

	queuedKind := distinctKind(t)
	queuedID, err := store.Enqueue(ctx, Item{Kind: queuedKind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue queued: %v", err)
	}

	sentKind := distinctKind(t)
	sentID, err := store.Enqueue(ctx, Item{Kind: sentKind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue sent: %v", err)
	}

	// #0389 -- both rows are queued and neither is claimed or sent yet, so
	// this is the one moment that discriminates Queued from Sent: two more
	// rows queued, zero more sent. A Counts that swaps which status feeds
	// which field cannot satisfy this under either label, unlike the
	// symmetric +1/+1 deltas asserted below on their own.
	mid, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (mid): %v", err)
	}
	if mid.Queued != before.Queued+2 {
		t.Fatalf("mid Queued = %d, want %d (before %d + both rows just enqueued, ids %d and %d, neither claimed nor sent yet)", mid.Queued, before.Queued+2, before.Queued, queuedID, sentID)
	}
	if mid.Sent != before.Sent {
		t.Fatalf("mid Sent = %d, want %d unchanged (before %d) -- nothing has been claimed or marked sent yet", mid.Sent, before.Sent, before.Sent)
	}

	// #0390 -- Sending and Abandoned are otherwise indistinguishable: every
	// other assertion in this package either never touches Sending at all
	// or asserts Abandoned as unchanged, so a Counts that swaps which SQL
	// argument feeds which field reports one's real value under the
	// other's name and the whole package stays green. Claiming a row and
	// deliberately leaving it mid-flight -- never calling MarkSent or
	// MarkRetryOrAbandon on it -- moves the real Sending count +1 and
	// leaves the real Abandoned count unchanged.
	//
	// This block must run BEFORE sentKind is claimed and marked sent below.
	// An earlier version measured the Sending delta only in the final
	// `after` snapshot, by which point sentID's own claim-then-MarkSent
	// round trip had also pushed the real Sending count up by a net +1 (it
	// passes through 'sending' on the way to 'sent') while the real Sent
	// count separately landed on +1 too -- so a Sending<->Sent swap
	// reported each field under the other's label with the same +1 delta
	// and passed undetected (measured directly: swapping StatusSending and
	// StatusSent in Counts' argument list left this test, and the rest of
	// the package, green). Taking the snapshot here instead, before sentID
	// is touched at all, makes the two deltas differ at the same instant --
	// Sending +1, Sent +0 -- the same shape as the Queued/Sent mid-point
	// above, and the only shape immune to that coincidence.
	sendingKind := distinctKind(t)
	sendingID, err := store.Enqueue(ctx, Item{Kind: sendingKind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue sending: %v", err)
	}
	sendingClaimed, err := store.ClaimDue(ctx, 10, []Kind{sendingKind})
	if err != nil {
		t.Fatalf("ClaimDue sendingKind: %v", err)
	}
	if len(sendingClaimed) != 1 || sendingClaimed[0].ID != sendingID {
		t.Fatalf("ClaimDue([]Kind{sendingKind}) claimed %v, want exactly the one row just enqueued for sendingKind (%d)", sendingClaimed, sendingID)
	}
	// Deliberately no MarkSent/MarkRetryOrAbandon call: sendingID stays
	// 'sending' for the rest of this test.

	midSending, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (midSending): %v", err)
	}
	if midSending.Sending != mid.Sending+1 {
		t.Fatalf("midSending Sending = %d, want %d (mid %d + the one row, id %d, just claimed and left mid-flight)", midSending.Sending, mid.Sending+1, mid.Sending, sendingID)
	}
	if midSending.Sent != mid.Sent {
		t.Fatalf("midSending Sent = %d, want %d unchanged (mid %d) -- sentID has not been claimed or marked sent yet", midSending.Sent, mid.Sent, mid.Sent)
	}

	claimed, err := store.ClaimDue(ctx, 10, []Kind{sentKind})
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != sentID {
		t.Fatalf("ClaimDue([]Kind{sentKind}) claimed %v, want exactly the one row just enqueued for sentKind (%d) -- claiming the queuedKind row too would leave this test unable to tell which row its own Queued delta is measuring", claimed, sentID)
	}
	if _, err := store.MarkSent(ctx, sentID, "msg-id"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	after, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (after): %v", err)
	}
	if after.Queued != before.Queued+1 {
		t.Fatalf("Queued = %d, want %d (before %d + the one row, id %d, left queued and never claimed)", after.Queued, before.Queued+1, before.Queued, queuedID)
	}
	if after.Sent != before.Sent+1 {
		t.Fatalf("Sent = %d, want %d (before %d + the one row, id %d, claimed and marked sent)", after.Sent, before.Sent+1, before.Sent, sentID)
	}
	if after.Sending != before.Sending+1 {
		t.Fatalf("Sending = %d, want %d (before %d + the one row, id %d, claimed and left mid-flight, never marked sent or abandoned)", after.Sending, before.Sending+1, before.Sending, sendingID)
	}
	if after.Abandoned != before.Abandoned {
		t.Fatalf("Abandoned moved from %d to %d after claiming a row into 'sending' and leaving it there -- claiming a row must never by itself count as a delivery-health failure", before.Abandoned, after.Abandoned)
	}
}

// TestOutbox_Counts_ReportsSkippedDistinctlyFromAbandoned is #0380's proof:
// Counts reports the 'skipped' terminal state as its own figure, and a
// MarkSkipped call moves Skipped without moving Abandoned — the whole point
// being that a skip is a correct outcome, not folded into the
// delivery-health figure. Measured as a delta around the one MarkSkipped
// call this test makes, matching TestOutbox_AbandonedCountByKind_ScopedToKind's
// own before/after idiom (this package's outbound_queue table is truncated
// once in TestMain, not between tests, so absolute counts are not scoped to
// this test alone).
func TestOutbox_Counts_ReportsSkippedDistinctlyFromAbandoned(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	kind := distinctKind(t)

	before, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (before): %v", err)
	}

	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10, []Kind{kind})
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("ClaimDue claimed %v, want exactly the one row just enqueued (%d)", rows, id)
	}
	done, err := store.MarkSkipped(ctx, id, "test: eligibility recheck declined")
	if err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if !done {
		t.Fatalf("MarkSkipped reported done=false for a freshly-claimed row")
	}

	after, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (after): %v", err)
	}
	if after.Skipped != before.Skipped+1 {
		t.Errorf("Skipped = %d, want %d (before %d + the one row just skipped)", after.Skipped, before.Skipped+1, before.Skipped)
	}
	if after.Abandoned != before.Abandoned {
		t.Errorf("Abandoned moved from %d to %d after a MarkSkipped call — a skip must never count as a delivery-health failure", before.Abandoned, after.Abandoned)
	}
	if after.Sent != before.Sent {
		t.Errorf("Sent moved from %d to %d after a MarkSkipped call, want unchanged", before.Sent, after.Sent)
	}
}

// TestOutbox_Counts_OldestQueuedAgeSecs is #0394's proof: Counts'
// OldestQueuedAgeSecs column, built from a `min(created_at) FILTER (WHERE
// status = $1)` clause, reports the age of the oldest QUEUED row and
// nothing else. #0390's review measured two mutations of that clause that
// left the whole package -- #0389's and #0390's own hardened
// TestOutbox_Counts included -- fully green: repointing the FILTER at
// StatusAbandoned, and dropping the FILTER entirely so the column reports
// the oldest row in ANY status. Neither of those five columns' assertions
// touches OldestQueuedAgeSecs at all, so nothing caught either mutation.
//
// Same remedy shape as #0389/#0390: a moment where the reported quantity is
// attributable to the queued row and to nothing else. This test backdates
// one queued row to a precisely known age (targetAgeSecs) and backdates one
// "poison" row per OTHER status -- sending, sent, abandoned, skipped -- to
// an age two orders of magnitude larger (poisonAgeSecs). A FILTER correctly
// scoped to StatusQueued alone reports targetAgeSecs regardless of the
// poison rows' presence. A FILTER repointed at any ONE of the four other
// statuses reports that status's own oldest row -- one of the poison
// rows -- which lands near poisonAgeSecs, not targetAgeSecs. A FILTER
// removed entirely reports the oldest row in the whole table, which is
// dominated by whichever poison row is oldest -- also near poisonAgeSecs,
// never targetAgeSecs. So the single assertion below is what #0394 calls
// for: it fires under repointing to any of the four other statuses AND
// under dropping the FILTER, and it does not fire on unmutated code,
// because the ~100000s gap between targetAgeSecs and poisonAgeSecs is far
// wider than the assertion's tolerance in either direction.
//
// created_at is set directly via SQL (the same technique
// TestOutbox_LatestByRecipients_ReturnsMostRecentPerRecipient already uses
// to backdate a row) rather than by sleeping (CLAUDE.md §5 / #394 criterion
// 6): the reported age is deterministic, and the assertion's tolerance
// below covers only the round trip between the UPDATE and the Counts()
// call that follows it, not any real elapsed wait -- so this test's running
// time does not depend on machine load.
func TestOutbox_Counts_OldestQueuedAgeSecs(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	before, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (before): %v", err)
	}

	// This package's outbound_queue is truncated once in TestMain, not
	// between tests (same idiom as TestOutbox_Counts above), so an earlier
	// test may have left queued rows behind with some nonzero age.
	// targetAgeSecs must exceed before.OldestQueuedAgeSecs by a margin much
	// larger than the few seconds elapsed since `before` was measured, so
	// backdating the row below to targetAgeSecs is guaranteed to make it
	// the new oldest queued row regardless of that residue.
	targetAgeSecs := before.OldestQueuedAgeSecs + 5000

	// poisonAgeSecs is targetAgeSecs plus another 100000s -- far enough that
	// no realistic scheduling delay or clock skew could make a poison row's
	// reported age land within the tolerance asserted below.
	poisonAgeSecs := targetAgeSecs + 100000

	queuedKind := distinctKind(t)
	queuedID, err := store.Enqueue(ctx, Item{Kind: queuedKind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue queued: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE outbound_queue SET created_at = now() - ($2 * interval '1 second') WHERE id = $1`,
		queuedID, targetAgeSecs,
	); err != nil {
		t.Fatalf("backdating queued row %d to age %ds: %v", queuedID, targetAgeSecs, err)
	}

	poisonStatuses := []string{StatusSending, StatusSent, StatusAbandoned, StatusSkipped}
	poisonIDs := make([]int64, len(poisonStatuses))
	for i, status := range poisonStatuses {
		kind := distinctKind(t)
		id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
		if err != nil {
			t.Fatalf("Enqueue poison row (status %q): %v", status, err)
		}
		poisonIDs[i] = id
		if _, err := pool.Exec(ctx,
			`UPDATE outbound_queue SET status = $2, created_at = now() - ($3 * interval '1 second') WHERE id = $1`,
			id, status, poisonAgeSecs,
		); err != nil {
			t.Fatalf("backdating poison row %d (status %q) to age %ds: %v", id, status, poisonAgeSecs, err)
		}
	}

	after, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts (after): %v", err)
	}

	// toleranceSecs covers only query round-trip time between the UPDATEs
	// above and this Counts() call -- not machine load (CLAUDE.md §5) -- and
	// is two orders of magnitude smaller than the gap to poisonAgeSecs, so
	// it cannot mask a repointed or removed FILTER.
	const toleranceSecs = 5
	diff := after.OldestQueuedAgeSecs - targetAgeSecs
	if diff < -toleranceSecs || diff > toleranceSecs {
		t.Fatalf("OldestQueuedAgeSecs = %d, want %d +/- %ds (the age backdated onto the queued row, id %d) -- got a value %ds away, which is far closer to the poison rows' age %d (ids %v, backdated into sending/sent/abandoned/skipped) than to the queued row's, meaning the FILTER is not scoped to StatusQueued alone", after.OldestQueuedAgeSecs, targetAgeSecs, toleranceSecs, queuedID, diff, poisonAgeSecs, poisonIDs)
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
//
// #0285 audit: this used real Kind constants (KindConfirmation,
// KindRegistration) and a relative before/after delta rather than an
// absolute count, which is why it was not the primary fix target — but
// internal/mailing's own outbox_worker_test.go abandons real
// KindConfirmation rows too, and a real Kind is shared surface this
// package does not own (see distinctKind's doc comment), so the "before"
// baseline is not this test's alone to reason about. Two kinds unique to
// this test close that gap entirely, the same way distinctKind does for
// the exact-claimed-count tests above.
func TestOutbox_AbandonedCountByKind_ScopedToKind(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	targetKind := distinctKind(t)
	otherKind := distinctKind(t)

	before, err := store.AbandonedCountByKind(ctx, targetKind)
	if err != nil {
		t.Fatalf("AbandonedCountByKind before: %v", err)
	}
	if before != 0 {
		t.Fatalf("AbandonedCountByKind(targetKind) before = %d, want 0 for a freshly-minted kind", before)
	}

	// Abandon an OTHER-kind row — must not move the target count.
	otherID, err := store.Enqueue(ctx, Item{Kind: otherKind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue other: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET status = $2 WHERE id = $1`, otherID, StatusAbandoned); err != nil {
		t.Fatalf("abandoning other-kind row: %v", err)
	}
	afterOther, err := store.AbandonedCountByKind(ctx, targetKind)
	if err != nil {
		t.Fatalf("AbandonedCountByKind after other-kind abandon: %v", err)
	}
	if afterOther != before {
		t.Errorf("AbandonedCountByKind(targetKind) moved from %d to %d after abandoning an OTHER-kind row", before, afterOther)
	}

	// Now abandon a target-kind row — must move the count by exactly 1.
	targetID, err := store.Enqueue(ctx, Item{Kind: targetKind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue target: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET status = $2 WHERE id = $1`, targetID, StatusAbandoned); err != nil {
		t.Fatalf("abandoning target-kind row: %v", err)
	}
	afterTarget, err := store.AbandonedCountByKind(ctx, targetKind)
	if err != nil {
		t.Fatalf("AbandonedCountByKind after target-kind abandon: %v", err)
	}
	if afterTarget != before+1 {
		t.Errorf("AbandonedCountByKind(targetKind) = %d after abandoning one target-kind row, want %d", afterTarget, before+1)
	}
}

// TestMarkSkipped_OnlyFromSending is #0365's proof for MarkSkipped's guard
// and payload-blanking: a claimed ('sending') row transitions to
// StatusSkipped with the reason in error, claimed_at cleared, and payload
// blanked to '{}'::jsonb — and a row NOT in 'sending' (never claimed, or
// already skipped) is left untouched, reported via done=false.
func TestMarkSkipped_OnlyFromSending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	kind := distinctKind(t)

	// A never-claimed ('queued') row must NOT be skippable — MarkSkipped
	// is guarded on status = 'sending' only, unlike MarkSent/MarkDone's
	// broader IN (...).
	queuedID, err := store.Enqueue(ctx, Item{
		Kind:      kind,
		Recipient: uniqueRecipient(t),
		Payload:   map[string]any{"confirm_token": "still-queued-token"},
	})
	if err != nil {
		t.Fatalf("Enqueue (queued): %v", err)
	}
	doneQueued, err := store.MarkSkipped(ctx, queuedID, "should not apply")
	if err != nil {
		t.Fatalf("MarkSkipped on queued row: %v", err)
	}
	if doneQueued {
		t.Fatalf("MarkSkipped on a still-queued row reported done=true")
	}
	var stillQueued string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, queuedID).Scan(&stillQueued); err != nil {
		t.Fatalf("select queued row: %v", err)
	}
	if stillQueued != StatusQueued {
		t.Fatalf("status = %q after a no-op MarkSkipped, want %q", stillQueued, StatusQueued)
	}

	// A claimed ('sending') row: the real path.
	id, err := store.Enqueue(ctx, Item{
		Kind:      kind,
		Recipient: uniqueRecipient(t),
		Payload:   map[string]any{"confirm_token": "secret-token-value"},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10, AllKinds)
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

	const reason = `subscriber status is now "complained", not "pending"`
	done, err := store.MarkSkipped(ctx, id, reason)
	if err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if !done {
		t.Fatalf("MarkSkipped reported done=false")
	}

	var status, payload string
	var errText *string
	var claimedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, payload::text, error, claimed_at FROM outbound_queue WHERE id = $1`, id,
	).Scan(&status, &payload, &errText, &claimedAt); err != nil {
		t.Fatalf("select after MarkSkipped: %v", err)
	}
	if status != StatusSkipped {
		t.Fatalf("status = %q, want %q", status, StatusSkipped)
	}
	if payload != "{}" {
		t.Fatalf("payload = %q, want scrubbed to {}", payload)
	}
	if errText == nil || *errText != reason {
		t.Fatalf("error = %v, want %q", errText, reason)
	}
	if claimedAt != nil {
		t.Fatalf("claimed_at = %v, want NULL after MarkSkipped", claimedAt)
	}

	// A second MarkSkipped on an already-skipped row must not report done
	// — a row can leave 'sending' by this path exactly once.
	done2, err := store.MarkSkipped(ctx, id, "ignored")
	if err != nil {
		t.Fatalf("second MarkSkipped: %v", err)
	}
	if done2 {
		t.Fatalf("second MarkSkipped on an already-skipped row reported done=true")
	}
}

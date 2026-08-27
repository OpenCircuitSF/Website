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

	// #0285: scoped to a kind unique to this test so the ClaimDue calls
	// below — which assert an exact claimed count — cannot observe a row
	// another package's concurrently-running test wrote to this same
	// shared outbound_queue table.
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
		rows, err := store.ClaimDue(ctx, 10, kind)
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

	// #0285: scoped to a kind unique to this test — see distinctKind's
	// doc comment for why an unscoped/shared-kind ClaimDue here can
	// observe another package's concurrently-running test.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10, kind)
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

	// #0285: scoped to a kind unique to this test — see distinctKind's
	// doc comment for why an unscoped/shared-kind ClaimDue here can
	// observe another package's concurrently-running test.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Attempt 1: claim and fail, leaving error populated on a 'queued' row.
	rows, err := store.ClaimDue(ctx, 10, kind)
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
	rows2, err := store.ClaimDue(ctx, 10, kind)
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
	// immune to internal/handlers' own concurrently-running
	// KindSubscribeIntake tests against this same shared table (see
	// distinctKind's doc comment).
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// The recovery poller claims the row and fails it, leaving it 'queued'
	// with error populated.
	rows, err := store.ClaimDue(ctx, 10, kind)
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
	if _, err := store.ClaimDue(ctx, 10, kind); err != nil {
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

	// #0285: scoped to a kind unique to this test — see distinctKind's
	// doc comment for why an unscoped ClaimDue here can observe another
	// package's concurrently-running test.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	rows, err := store.ClaimDue(ctx, 10, kind)
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
	rows2, err := store.ClaimDue(ctx, 10)
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

	// #0285: scoped to a kind unique to this test — see distinctKind's
	// doc comment for why an unscoped ClaimDue here can observe another
	// package's concurrently-running test.
	kind := distinctKind(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	rows, err := store.ClaimDue(ctx, 10, kind)
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
//
// #0285: the sweep and its exact swept-count assertion are scoped to a
// kind unique to this test (see distinctKind's doc comment) — the table is
// shared across packages within a test run, and an earlier version of this
// test asserted swept==1 from an UNSCOPED sweep, which counted every stale
// 'sending' row in the whole table, including ones a different package's
// concurrently-running test had left behind. That is not the CLAUDE.md §5
// "machine is busy" class of flake: it fails on an idle machine too, given
// the wrong interleaving, because it asserts a count over state this test
// does not own. TestOutbox_OrphanSweep_Unscoped_SweepsAcrossKinds below
// keeps the unscoped code path covered without depending on an exact total.
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

	if _, err := store.ClaimDue(ctx, 10, kind); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	// Backdate only the "stale" row's claim.
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET claimed_at = now() - interval '1 hour' WHERE id = $1`, staleID); err != nil {
		t.Fatalf("backdating claimed_at: %v", err)
	}

	swept, err := store.OrphanSweep(ctx, 5*time.Minute, kind)
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
// definition affects rows this test does not own, in any concurrently
// running package's test against the same shared database, so an exact
// total is exactly the assertion #0285 removed above. Instead it proves
// the two properties that matter and belong to this test's own rows: its
// stale row is included in an unscoped sweep (swept >= 1 is too weak on
// its own — a concurrent sweep from another package's test could
// coincidentally produce a nonzero count without ever having touched this
// row — so the row's own resulting status is checked directly), and its
// live row is not.
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

	if _, err := store.ClaimDue(ctx, 10, kind); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_queue SET claimed_at = now() - interval '1 hour' WHERE id = $1`, staleID); err != nil {
		t.Fatalf("backdating claimed_at: %v", err)
	}

	if _, err := store.OrphanSweep(ctx, 5*time.Minute); err != nil {
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
//
// #0285 audit: this used real Kind constants (KindConfirmation,
// KindRegistration) and a relative before/after delta rather than an
// absolute count, which is why it was not the primary fix target — but
// internal/mailing's own outbox_worker_test.go abandons real
// KindConfirmation rows too, so a concurrently-running test in that
// package could still land inside this test's before/after window and
// move the "before" baseline out from under it. Two kinds unique to this
// test close that window entirely, the same way distinctKind does for the
// exact-claimed-count tests above.
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

package outbox

import (
	"context"
	"testing"
	"time"
)

// select_due_claim_row_test.go is #0297's regression coverage for the
// per-row claim path (SelectDue + ClaimRow, store.go) that
// internal/mailing.OutboxWorker.pass and
// internal/handlers.SubscribeHandler.intakePass were moved onto — see
// ClaimDue's own doc comment for why ClaimDue itself was left unchanged
// rather than reimplemented in terms of this pair.

// TestOutbox_SelectDue_LeavesRowsQueuedUntilClaimRow is the architectural
// proof every one of the three orphan-window derivations that reference
// #0297 now depends on: SelectDue must not claim anything. Mirrors
// internal/mailing's TestSendStore_ClaimBatch_LeavesRowsQueuedUntilClaimRow
// (#0295) exactly, applied to outbound_queue instead of email_sends. If
// SelectDue is ever changed to stamp claimed_at the way ClaimDue does, this
// test fails immediately — the signal that whichever orphan window depends
// on the per-row model would need the batch-aware treatment instead.
func TestOutbox_SelectDue_LeavesRowsQueuedUntilClaimRow(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	kind := distinctKind(t)

	recipient := uniqueRecipient(t)
	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: recipient})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	ids, err := store.SelectDue(ctx, 10, kind)
	if err != nil {
		t.Fatalf("SelectDue: %v", err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("SelectDue() ids = %v, want exactly [%d]", ids, id)
	}

	var status string
	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, claimed_at FROM outbound_queue WHERE id = $1`, id).
		Scan(&status, &claimedAt); err != nil {
		t.Fatalf("select after SelectDue: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("status after SelectDue = %q, want %q — SelectDue must not claim rows itself", status, StatusQueued)
	}
	if claimedAt != nil {
		t.Fatalf("claimed_at after SelectDue = %v, want NULL", claimedAt)
	}

	// ClaimRow is the only thing that claims, and it does so per row.
	row, claimed, err := store.ClaimRow(ctx, id)
	if err != nil || !claimed {
		t.Fatalf("ClaimRow after SelectDue: claimed=%v err=%v", claimed, err)
	}
	if row.Status != StatusSending || row.ClaimedAt == nil {
		t.Fatalf("ClaimRow() row = %+v, want status=sending and a stamped ClaimedAt", row)
	}
	if row.Attempts != 1 {
		t.Fatalf("ClaimRow() Attempts = %d, want 1", row.Attempts)
	}
}

// TestOutbox_ClaimRow_LosingRaceReturnsClaimedFalse proves the exclusivity
// half of the split: once one caller's ClaimRow succeeds, a second call for
// the same id must not also succeed — mirroring MarkSent/MarkDone's own
// "affects zero rows is not an error" convention (store.go).
func TestOutbox_ClaimRow_LosingRaceReturnsClaimedFalse(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	kind := distinctKind(t)

	id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if _, claimed, err := store.ClaimRow(ctx, id); err != nil || !claimed {
		t.Fatalf("first ClaimRow: claimed=%v err=%v", claimed, err)
	}
	row, claimed, err := store.ClaimRow(ctx, id)
	if err != nil {
		t.Fatalf("second ClaimRow: %v", err)
	}
	if claimed {
		t.Fatalf("second ClaimRow claimed=%v, want false — the row was already 'sending'", claimed)
	}
	if row.ID != 0 {
		t.Fatalf("second ClaimRow row.ID = %d, want 0 (the zero value) on a lost race", row.ID)
	}

	// A non-existent id behaves identically — claimed=false, not an error.
	row, claimed, err = store.ClaimRow(ctx, id+1_000_000_000)
	if err != nil || claimed {
		t.Fatalf("ClaimRow on a nonexistent id: claimed=%v err=%v, want claimed=false err=nil", claimed, err)
	}
	if row.ID != 0 {
		t.Fatalf("ClaimRow on a nonexistent id: row.ID = %d, want 0 (the zero value)", row.ID)
	}
}

// TestOutbox_SelectThenClaimRow_CrashMidBatch is #0297 criterion 6's
// DB-backed proof of the crash path: simulate a worker that SelectDue'd a
// batch of three, ClaimRow'd (and is still "processing") the first, and
// then crashed before reaching the other two.
//
//   - The two rows never reached ClaimRow are still plain 'queued' — never
//     having been marked 'sending' at all, they need no sweep to become
//     reclaimable: a fresh SelectDue+ClaimRow (standing in for the next
//     poll pass, by this worker or another) claims them immediately. This
//     is the direct, structural consequence of #0297's design: a row
//     waiting its turn in a caller's serial loop is never at risk, because
//     it was never claimed in the first place.
//   - The one row that WAS claimed and is genuinely still fresh (a small
//     staleAfter that has not yet elapsed) is untouched by OrphanSweep —
//     "still being processed" must not be swept.
//   - The same claimed row, once its claim genuinely predates staleAfter,
//     IS reclaimed by OrphanSweep — proving the mechanism cuts both ways,
//     not just "never sweeps anything."
func TestOutbox_SelectThenClaimRow_CrashMidBatch(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	kind := distinctKind(t)

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := store.Enqueue(ctx, Item{Kind: kind, Recipient: uniqueRecipient(t)})
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	selected, err := store.SelectDue(ctx, 10, kind)
	if err != nil {
		t.Fatalf("SelectDue: %v", err)
	}
	if len(selected) != 3 {
		t.Fatalf("SelectDue() = %v, want 3 ids", selected)
	}

	// "Crash mid-batch": claim only the first row, exactly as a worker's
	// serial loop would have done before dying.
	claimedRow, claimed, err := store.ClaimRow(ctx, selected[0])
	if err != nil || !claimed {
		t.Fatalf("ClaimRow(%d): claimed=%v err=%v", selected[0], claimed, err)
	}

	// Unprocessed rows are reclaimable promptly — no sweep needed, because
	// they were never claimed. A fresh SelectDue+ClaimRow (standing in for
	// the very next poll pass) picks them straight up; each is then marked
	// done immediately (standing in for that pass finishing them) so the
	// OrphanSweep assertions below are scoped to claimedRow alone, not
	// these two as well.
	for _, id := range selected[1:] {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("select status for unprocessed row %d: %v", id, err)
		}
		if status != StatusQueued {
			t.Fatalf("unprocessed row %d status = %q, want %q — SelectDue must never have claimed it", id, status, StatusQueued)
		}
		if _, claimed, err := store.ClaimRow(ctx, id); err != nil || !claimed {
			t.Fatalf("reclaiming unprocessed row %d: claimed=%v err=%v", id, claimed, err)
		}
		if done, err := store.MarkSent(ctx, id, "test-message-id"); err != nil || !done {
			t.Fatalf("finishing reclaimed row %d: done=%v err=%v", id, done, err)
		}
	}

	// The row still genuinely being "processed" — a generous staleAfter —
	// must not be swept.
	swept, err := store.OrphanSweep(ctx, time.Hour, kind)
	if err != nil {
		t.Fatalf("OrphanSweep (fresh claim, generous window): %v", err)
	}
	if swept != 0 {
		t.Fatalf("OrphanSweep with a generous window swept %d row(s), want 0 — a genuinely live claim must not be released", swept)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, claimedRow.ID).Scan(&status); err != nil {
		t.Fatalf("select status for still-claimed row: %v", err)
	}
	if status != StatusSending {
		t.Fatalf("still-claimed row status = %q, want %q after a sweep that must not have touched it", status, StatusSending)
	}

	// The same claim, once staleAfter=0 treats it as immediately stale
	// (simulating a real crash: the process that held it is now long
	// gone), IS reclaimed — proving the mechanism actually protects only a
	// genuinely live claim, not every claim unconditionally.
	swept, err = store.OrphanSweep(ctx, 0, kind)
	if err != nil {
		t.Fatalf("OrphanSweep (stale window): %v", err)
	}
	if swept != 1 {
		t.Fatalf("OrphanSweep with a zero-width window swept %d row(s), want 1", swept)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM outbound_queue WHERE id = $1`, claimedRow.ID).Scan(&status); err != nil {
		t.Fatalf("select status after sweep: %v", err)
	}
	if status != StatusQueued {
		t.Fatalf("swept row status = %q, want %q", status, StatusQueued)
	}
}

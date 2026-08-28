package mailing

import "testing"

// outbox_orphan_stale_after_test.go was #0284's regression coverage for a
// batch-derived outboxOrphanStaleAfter; #0297 replaced the batch-wide
// claim model itself (SelectDue/ClaimRow, internal/outbox/store.go — see
// outbox_worker.go's pass), so the invariant worth checking is now the
// single-row one, not a batch multiple. Renamed from
// TestOutboxOrphanStaleAfterCoversFullBatch accordingly ("FullBatch" no
// longer describes the model at all) rather than deleted, per this
// project's "update, don't delete" convention for a superseded invariant
// test (CLAUDE.md / issues/0297.md criterion 5).
//
// Modeled directly on worker.go's own
// TestOrphanStaleAfterCoversLegitimateSingleRowHold (#0295) — the two
// workers' windows now share the identical shape, since #0297 gave this
// worker the same select-then-per-row-claim model worker.go always had.
// The oracle is a DIFFERENT expression from the subject (no "2 *" margin
// factor), not the same bytes with a name change (CLAUDE.md's "a guard's
// oracle must not be the same bytes as its subject", #0258): an edit to
// the subject that drops or shrinks its margin factor changes `got`
// without moving `want`.
//
// Proof this fails against reversion (verified by hand in a scratch copy,
// not left in the tree — CLAUDE.md's "an oracle must not share its method
// with its subject" applies equally to leaving a reverted formula
// sitting nearby): pointing `got` at the pre-#0297 800s batch-derived
// value against today's oracle still passes trivially (800s > 35s), which
// is exactly why the OLD test (a batch-worst-case oracle) is the one that
// actually exercised the regression this fix closes — reverting
// outboxOrphanStaleAfter to a value AT OR BELOW the single-row bound
// (e.g. sendMessageTimeout alone, 30s, omitting writeStatusTimeout) makes
// this test fail immediately: "outboxOrphanStaleAfter (30s) does not
// exceed the legitimate worst-case hold time of a single live row (35s)".
func TestOutboxOrphanStaleAfterCoversSingleRowHold(t *testing.T) {
	got := outboxOrphanStaleAfter
	legitimateSingleRowHold := sendMessageTimeout + writeStatusTimeout
	if got <= legitimateSingleRowHold {
		t.Fatalf("outboxOrphanStaleAfter (%s) does not exceed the legitimate worst-case hold time of a single live row (%s) — OrphanSweep could release a still-live claim, leading to a duplicate send (#0254)",
			got, legitimateSingleRowHold)
	}
}

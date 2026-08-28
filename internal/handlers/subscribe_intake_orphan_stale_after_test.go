package handlers

import "testing"

// subscribe_intake_orphan_stale_after_test.go was #0294's regression
// coverage for a batch-derived intakeOrphanStaleAfter; #0297 replaced the
// batch-wide claim model itself (SelectDue/ClaimRow,
// internal/outbox/store.go — see subscribe_intake.go's intakePass), so the
// invariant worth checking is now a single-row one, not a batch multiple.
// Renamed from TestIntakeOrphanStaleAfterCoversFullBatch accordingly
// rather than deleted, per this project's "update, don't delete"
// convention for a superseded invariant test (CLAUDE.md /
// issues/0297.md criterion 5).
//
// Modeled directly on internal/mailing's own
// TestOrphanStaleAfterCoversLegitimateSingleRowHold (worker.go/#0295) and
// its sibling TestOutboxOrphanStaleAfterCoversSingleRowHold (#0297): the
// oracle checks the window against ONE row-processing term
// (intakeRowTimeout), WITHOUT the subject's own doubling — a different
// expression from the subject's `2 * intakeRowTimeout` initializer, not
// the same bytes with a name change (CLAUDE.md's "a guard's oracle must
// not be the same bytes as its subject", #0258): an edit to the subject
// that drops or shrinks its margin factor changes `got` without changing
// `want`.
//
// Proof this fails against reversion (verified by hand in a scratch copy,
// not left in the tree): pointing `got` at the pre-#0297 batch-derived
// formula, (intakeBatchSize+1)*intakeRowTimeout = 210s, against today's
// oracle still passes trivially (210s > 10s) — the OLD test (a
// batch-worst-case oracle) is what actually exercised THAT regression.
// Reverting intakeOrphanStaleAfter to intakeRowTimeout alone (10s, no
// margin for the ClaimRow round-trip term) makes THIS test fail
// immediately: "intakeOrphanStaleAfter (10s) does not exceed the
// legitimate worst-case hold time of a single live row (10s)".
func TestIntakeOrphanStaleAfterCoversSingleRowHold(t *testing.T) {
	got := intakeOrphanStaleAfter
	legitimateSingleRowHold := intakeRowTimeout
	if got <= legitimateSingleRowHold {
		t.Fatalf("intakeOrphanStaleAfter (%s) does not exceed the legitimate worst-case hold time of a single live row (%s) — OrphanSweep could release a still-live claim, leading to a duplicate mutation dispatch (#0254)",
			got, legitimateSingleRowHold)
	}
}

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
// #0297's review bounce (defect 3): this test's oracle was originally
// intakeRowTimeout alone (10s) — HALF the 2*intakeRowTimeout (20s) worst
// case the doc comment (subscribe_intake.go) actually derives from
// intakePass's own bounded ClaimRow round trip plus processIntakeRow's
// bounded reprocessing. That oracle would still pass at 11s, well inside
// the real 20s worst case — the invariant it checked was not the
// invariant that was derived. The oracle here is now the FULL derived
// worst case, 2 * intakeRowTimeout, so it actually enforces what the doc
// comment claims. It remains a different expression from the subject's
// `3 * intakeRowTimeout` initializer, not the same bytes with a name
// change (CLAUDE.md's "a guard's oracle must not be the same bytes as its
// subject", #0258): an edit to the subject that drops its margin term (the
// extra `* intakeRowTimeout`) changes `got` without moving `want`. Modeled
// on the same shape as internal/mailing's
// TestOrphanStaleAfterCoversLegitimateSingleRowHold (worker.go/#0295) and
// TestOutboxOrphanStaleAfterCoversSingleRowHold
// (outbox_orphan_stale_after_test.go/#0297): oracle = the worst case
// WITHOUT the subject's own margin term.
//
// Proof this fails against reversion (verified by hand in a scratch copy,
// not left in the tree):
//   - pointing `got` at the pre-#0297 batch-derived formula,
//     (intakeBatchSize+1)*intakeRowTimeout = 210s, against today's oracle
//     still passes trivially (210s > 20s) — the OLD full-batch test (a
//     batch-worst-case oracle) is what actually exercised THAT regression.
//   - pointing `got` at intakeRowTimeout alone (10s, dropping both margin
//     AND one of the two derived terms) fails, as expected.
//   - reverting intakeOrphanStaleAfter to exactly 2 * intakeRowTimeout
//     (20s — the zero-margin value defect 3 itself bounced on) now ALSO
//     fails against this oracle: "intakeOrphanStaleAfter (20s) does not
//     exceed the legitimate worst-case hold time of a single live row
//     (20s)" — which is the point: the zero-margin regression is no
//     longer invisible to this test.
func TestIntakeOrphanStaleAfterCoversSingleRowHold(t *testing.T) {
	got := intakeOrphanStaleAfter
	legitimateSingleRowHold := 2 * intakeRowTimeout
	if got <= legitimateSingleRowHold {
		t.Fatalf("intakeOrphanStaleAfter (%s) does not exceed the legitimate worst-case hold time of a single live row (%s) — OrphanSweep could release a still-live claim, leading to a duplicate mutation dispatch (#0254)",
			got, legitimateSingleRowHold)
	}
}

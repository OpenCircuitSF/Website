package handlers

import (
	"testing"
	"time"
)

// subscribe_intake_orphan_stale_after_test.go is #0294's regression
// coverage: intakeOrphanStaleAfter (subscribe_intake.go) must cover
// (be >=) the worst-case age of the LAST row in a full intakeBatchSize
// batch, not just one row's own worst case — see that var's doc comment
// for the full derivation this file checks. The window and the worst case
// are equal at today's constants by design (intakeOrphanStaleAfter charges
// every row in the batch its own full intakeRowTimeout bound, plus one
// more intakeRowTimeout for the ClaimDue call that stamps the batch's
// shared claimed_at, with no additional margin term), so the assertion is
// >=, not a strict >.
//
// Modeled on internal/mailing/outbox_orphan_stale_after_test.go
// (#0284's regression coverage for the sibling window), including its
// central lesson (CLAUDE.md's "a guard's oracle must not be the same
// bytes as its subject", from #0258): the oracle below is built the
// OPPOSITE way round from the subject. The subject is one flat
// multiplication ((batchSize+1) * perRowBound); the oracle is a sum of
// (batchSize+1) individual per-row terms, computed in a loop rather than
// folded into one multiplication. Different code shape, same arithmetic
// result when the subject is correct — which is exactly what makes it
// able to catch a subject that goes back to charging something other
// than a full per-row bound per predecessor, or that drops the extra
// ClaimDue term entirely (the pre-#0294 defect: 2 * intakeRowTimeout,
// which charged only one row's worth twice, ignoring the batch).
func intakeWorstCaseFullBatchAge(batchSize int, perRowBound time.Duration) time.Duration {
	var total time.Duration
	// +1: intakePass's ClaimDue call stamps the whole batch's shared
	// claimed_at, then must still return control (its own RETURNING scan)
	// before row 1's own clock starts — see intakeOrphanStaleAfter's doc
	// comment for why that gap is bounded by the same intakeRowTimeout
	// constant and is charged as one more term, not folded into the batch
	// loop below.
	for i := 0; i < batchSize+1; i++ {
		total += perRowBound
	}
	return total
}

// TestIntakeOrphanStaleAfterCoversFullBatch is the invariant #0294
// criterion 3 asks for: the orphan window must exceed the worst-case age
// of the last row in a full batch, computed from the REAL package
// constants (intakeBatchSize, intakeRowTimeout) rather than copied
// literals — so it is sensitive to exactly the regression the issue
// describes. If a future change raises intakeBatchSize (or
// intakeRowTimeout) without intakeOrphanStaleAfter's derivation picking
// that change up automatically — e.g. someone "simplifies" it back to a
// hand-computed literal, or reverts to charging only one row's bound —
// this test recomputes the worst case with the NEW real constants and the
// invariant breaks, because it is reading the same source of truth
// intakeOrphanStaleAfter itself is supposed to be reading.
//
// Proof this fails against reversion: pointing `got` at the pre-#0294
// formula, 2*intakeRowTimeout (20s), against today's oracle (210s) fails
// immediately — 20s does not cover 210s. Proof this does NOT false-fail on
// a legitimate config change (the exact failure #0284's reviewer found in
// a test of this shape, per this issue's own warning): raising
// intakeBatchSize alone, with intakeOrphanStaleAfter's real derivation
// left in place, moves BOTH sides of the comparison together (the subject
// via intakeBatchSize in its own formula, the oracle via the batchSize
// argument this test passes it), so the invariant keeps holding rather
// than spuriously breaking. Both checked by hand in a throwaway worktree
// before this file was committed:
//
//   - reverted intakeOrphanStaleAfter to 2*intakeRowTimeout in a scratch
//     copy, ran this test: FAILed, "intakeOrphanStaleAfter (20s) does not
//     cover the worst-case age of the last row in a full 20-row batch
//     (3m30s)".
//   - restored the real file, then edited ONLY intakeBatchSize to 40 in a
//     scratch copy (leaving intakeOrphanStaleAfter's derivation — the var
//     initializer — untouched): intakeOrphanStaleAfter recomputed to
//     (40+1)*10s = 410s, this test's oracle recomputed to the same 410s
//     via intakeWorstCaseFullBatchAge(40, intakeRowTimeout) — PASSED, not
//     a false failure.
//
// Restored the real file afterward and confirmed the hash matched the
// pre-mutation copy before re-running the suite.
func TestIntakeOrphanStaleAfterCoversFullBatch(t *testing.T) {
	got := intakeOrphanStaleAfter
	want := intakeWorstCaseFullBatchAge(intakeBatchSize, intakeRowTimeout)
	if got < want {
		t.Fatalf("intakeOrphanStaleAfter (%s) does not cover the worst-case age of the last row in a full %d-row batch (%s) — OrphanSweep could release a still-live claim, leading to a duplicate mutation dispatch (#0254)",
			got, intakeBatchSize, want)
	}
}

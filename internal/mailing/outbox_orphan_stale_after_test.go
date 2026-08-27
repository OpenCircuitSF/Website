package mailing

import (
	"testing"
	"time"
)

// outbox_orphan_stale_after_test.go is #0284's regression coverage:
// outboxOrphanStaleAfter (outbox_worker.go) must exceed the worst-case age
// of the LAST row in a full outboxDefaultBatchSize batch, not just one
// message's own worst case — see that var's doc comment for the full
// derivation this file checks.

// outboxWorstCaseFullBatchAge independently computes how old the last row
// in a full batch of batchSize can legitimately be by the time its own
// send finishes: (batchSize-1) rate-limiter intervals at minSendRate,
// waiting behind every predecessor, plus that row's own worst-case send
// (sendMessageTimeout + writeStatusTimeout).
//
// Deliberately a DIFFERENT arithmetic expression from
// outboxOrphanStaleAfter's (batchSize-1 here, batchSize in
// outbox_worker.go) rather than a call into shared production code: an
// oracle built from the same expression as its subject agrees with the
// subject by construction and proves nothing (CLAUDE.md's "a guard's
// oracle must not be the same bytes as its subject", from #0258). If a
// future edit collapses outboxOrphanStaleAfter's own formula to also use
// batchSize-1 (removing the deliberate one-interval margin), this
// function's independently-written "-1" no longer differs from the
// subject's, TestOutboxOrphanStaleAfterCoversFullBatch's `>` becomes `==`,
// and the test correctly fails.
func outboxWorstCaseFullBatchAge(batchSize, minSendRate int) time.Duration {
	interval := time.Second / time.Duration(minSendRate)
	return time.Duration(batchSize-1)*interval + sendMessageTimeout + writeStatusTimeout
}

// TestOutboxOrphanStaleAfterCoversFullBatch is the invariant #0284
// criterion 3 asks for: the orphan window must exceed the worst-case age
// of the last row in a full batch, computed from the REAL package
// constants (outboxDefaultBatchSize, outboxMinSendRate,
// sendMessageTimeout, writeStatusTimeout) rather than copied literals — so
// it is sensitive to exactly the regression the issue describes. If a
// future change raises outboxDefaultBatchSize (or lowers
// outboxMinSendRate, or raises either timeout) without outboxOrphanStale
// After's derivation picking that change up automatically — e.g. someone
// "simplifies" it back to a hand-computed literal — this test recomputes
// the worst case with the NEW real constant and the invariant breaks,
// because it is reading the same source of truth outboxOrphanStaleAfter
// itself is supposed to be reading.
func TestOutboxOrphanStaleAfterCoversFullBatch(t *testing.T) {
	got := outboxOrphanStaleAfter
	want := outboxWorstCaseFullBatchAge(outboxDefaultBatchSize, outboxMinSendRate)
	if got <= want {
		t.Fatalf("outboxOrphanStaleAfter (%s) does not exceed the worst-case age of the last row in a full %d-row batch at the %d msg/sec floor (%s) — OrphanSweep could release a still-live claim, leading to a duplicate send (#0254)",
			got, outboxDefaultBatchSize, outboxMinSendRate, want)
	}
}

// TestNonBatchAwareWindowFailsAsBatchGrows is #0284 criterion 3's second
// half: proof that a FIXED, non-batch-aware window — exactly the shape of
// the pre-#0284 derivation, 2 * (sendMessageTimeout + writeStatusTimeout) —
// is not a safe substitute for outboxOrphanStaleAfter's batch-derived
// formula, regardless of what number it starts at. It happens to still
// cover TODAY's worst case (the "accidental margin" #0284's description
// calls out — the pre-#0284 70s did cover the actual ~54s worst case, by
// luck rather than by derivation), but a window that does not move when
// the batch size does is not actually protected by that margin: it erodes
// silently as soon as the batch grows enough. This test raises the batch
// size (not the window) and shows the naive constant stops covering the
// worst case, which is exactly "must fail if someone raises the batch
// size without touching the window" (#0284 criterion 3) — demonstrated
// directly rather than argued.
func TestNonBatchAwareWindowFailsAsBatchGrows(t *testing.T) {
	naiveWindow := 2 * (sendMessageTimeout + writeStatusTimeout)

	todaysWorstCase := outboxWorstCaseFullBatchAge(outboxDefaultBatchSize, outboxMinSendRate)
	if naiveWindow <= todaysWorstCase {
		t.Fatalf("test setup invariant broken: the naive pre-#0284 window (%s) no longer covers today's worst case (%s) on its own — the 'accidental margin' this test demonstrates the fragility of no longer holds, so this test can't demonstrate what it's meant to; re-derive grownBatch's multiplier", naiveWindow, todaysWorstCase)
	}

	const grownBatch = outboxDefaultBatchSize * 10
	grownWorstCase := outboxWorstCaseFullBatchAge(grownBatch, outboxMinSendRate)
	if naiveWindow > grownWorstCase {
		t.Fatalf("naive, non-batch-aware window (%s) still covers the worst case for a %d-row batch (%s) — strengthen grownBatch so this test actually exercises the silent erosion #0284 closes", naiveWindow, grownBatch, grownWorstCase)
	}
	// naiveWindow has now failed to cover a grown batch's worst case,
	// confirming a fixed constant is not a safe substitute for deriving
	// the window from the batch size — which is exactly why
	// outboxOrphanStaleAfter is a formula over outboxDefaultBatchSize,
	// not a literal.
}

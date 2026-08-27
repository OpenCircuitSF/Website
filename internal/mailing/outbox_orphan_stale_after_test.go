package mailing

import (
	"testing"
	"time"
)

// outbox_orphan_stale_after_test.go is #0284's regression coverage:
// outboxOrphanStaleAfter (outbox_worker.go) must cover (be >=) the
// worst-case age of the LAST row in a full outboxDefaultBatchSize batch,
// not just one message's own worst case — see that var's doc comment for
// the full derivation this file checks. The window and the worst case are
// equal at today's constants by design (outboxOrphanStaleAfter charges
// every row in the batch its own full worst-case bound, with no
// additional margin term), so the assertion is >=, not a strict >.
//
// #0284's review bounced a first version of this file: its oracle charged
// each of (batchSize-1) predecessors the SAME per-predecessor term the
// subject did (a rate-limiter interval), so it was the subject's own
// formula minus one term rather than an independent check, and it could
// not catch the defect the subject actually had (CLAUDE.md's "a guard's
// oracle must not be the same bytes as its subject", from #0258). The
// oracle below is built the opposite way round from the subject: the
// subject is one flat multiplication (batchSize * perRowBound); the
// oracle is a sum of batchSize individual per-row terms, computed in a
// loop rather than folded into one multiplication. Different code shape,
// same arithmetic result when the subject is correct — which is exactly
// what makes it able to catch a subject that goes back to charging
// something other than a full per-row bound per predecessor (e.g. a
// rate-limiter interval): the loop's answer would then diverge from the
// subject's instead of agreeing with it by construction.
func outboxWorstCaseFullBatchAge(batchSize int, perRowBound time.Duration) time.Duration {
	var total time.Duration
	for i := 0; i < batchSize; i++ {
		total += perRowBound
	}
	return total
}

// TestOutboxOrphanStaleAfterCoversFullBatch is the invariant #0284
// criterion 3 asks for: the orphan window must exceed the worst-case age
// of the last row in a full batch, computed from the REAL package
// constants (outboxDefaultBatchSize, sendMessageTimeout,
// writeStatusTimeout) rather than copied literals — so it is sensitive to
// exactly the regression the issue describes. If a future change raises
// outboxDefaultBatchSize (or raises either timeout) without
// outboxOrphanStaleAfter's derivation picking that change up
// automatically — e.g. someone "simplifies" it back to a hand-computed
// literal, or reintroduces a rate-limiter term that lets the window fall
// below a full per-row multiple — this test recomputes the worst case
// with the NEW real constants and the invariant breaks, because it is
// reading the same source of truth outboxOrphanStaleAfter itself is
// supposed to be reading.
//
// Proof this fails against reversion: pointing `got` at the pre-fix
// formula, time.Duration(outboxDefaultBatchSize)*(time.Second/1) +
// sendMessageTimeout + writeStatusTimeout (55s), against today's oracle
// (800s) fails immediately — 55s does not exceed 800s. Verified by hand
// in a scratch copy before this file was committed; not left in the
// tree, since a permanent copy of the reverted formula would itself be
// another "same premise" oracle risk.
func TestOutboxOrphanStaleAfterCoversFullBatch(t *testing.T) {
	got := outboxOrphanStaleAfter
	want := outboxWorstCaseFullBatchAge(outboxDefaultBatchSize, sendMessageTimeout+2*writeStatusTimeout)
	if got < want {
		t.Fatalf("outboxOrphanStaleAfter (%s) does not cover the worst-case age of the last row in a full %d-row batch (%s) — OrphanSweep could release a still-live claim, leading to a duplicate send (#0254)",
			got, outboxDefaultBatchSize, want)
	}
}

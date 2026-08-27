package mailing

import "testing"

// orphan_stale_after_test.go is #0295's regression coverage for
// worker.go's orphanStaleAfter — see that var's doc comment for the full
// derivation this file checks, and worker_store_test.go's
// TestSendStore_ClaimBatch_LeavesRowsQueuedUntilClaimRow for the
// companion DB-backed test proving the architectural premise (ClaimBatch
// never stamps claimed_at) this test's arithmetic depends on.
//
// Unlike outbox_worker.go's outboxOrphanStaleAfter (#0284), there is no
// batch multiplication to check here — #0295 found worker.go's ClaimRow
// re-stamps claimed_at per row, so the invariant that actually matters is
// simpler: the window must exceed a single live row's own legitimate
// worst-case hold time, sendMessageTimeout + writeStatusTimeout. The
// oracle below computes that bound directly, WITHOUT the subject's own
// "2 *" margin factor — a different expression from the subject, not the
// same bytes with a name change (CLAUDE.md's "a guard's oracle must not
// be the same bytes as its subject", from #0258): an edit to the subject
// that drops or shrinks the margin factor changes `got` without changing
// `want`, so the two diverge instead of moving together.
func TestOrphanStaleAfterCoversLegitimateSingleRowHold(t *testing.T) {
	got := orphanStaleAfter
	legitimateSingleRowHold := sendMessageTimeout + writeStatusTimeout
	if got <= legitimateSingleRowHold {
		t.Fatalf("orphanStaleAfter (%s) does not exceed the legitimate worst-case hold time of a single live row (%s) — OrphanSweep could release a still-live claim, leading to a duplicate send (#0254)",
			got, legitimateSingleRowHold)
	}
}

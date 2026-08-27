// subscribe_intake.go is #0254's durability backstop for
// SubscribeHandler.mutateQueue — the in-memory, per-process channel #0088
// introduced to keep POST /api/subscribe's response uniform (CLAUDE.md §9)
// by moving every branch-dependent write off the request goroutine. #0126
// made "committed signup ⇒ queued confirmation" durable (the INSERT and the
// outbound_queue enqueue share one transaction in internal/subscribers.Store),
// but it explicitly did NOT make "accepted 202 ⇒ committed signup" durable —
// see that issue's own "Non-goal" note. mutateQueue can still lose an
// already-accepted request two ways: the channel is full (enqueueMutation's
// default case), or the process is killed with a job still queued or
// in-flight (Close's bounded wait can time out, and a hard crash skips Close
// entirely).
//
// # The fix: a synchronous, unconditional durable write before the response
//
// Subscribe (subscribe.go) writes one outbox.KindSubscribeIntake row per
// accepted request, synchronously, before writing the 202 — see that
// method's own comment for why this is safe against CLAUDE.md §9's
// uniform-202/timing rule (it is a third fixed-cost step, identical in
// shape for every branch, exactly like the suppression/FindByEmail reads
// #0088 already established). By the time a client observes the 202, that
// row is committed. If mutateQueue then processes the job normally
// (the overwhelming common case — see intakeGraceDelay below), the fast
// path marks the row done itself (markIntakeDone, subscribe.go). If it
// doesn't — queue-full, or the process dies before mutateQueue ever drains
// it — the row is left 'queued' in Postgres, and THIS file's recovery
// poller (runIntakeWorker) will eventually claim and reprocess it. Either
// way, "a 202 was returned" now has a durable, reconcilable record: never
// silently lost, never a signup the subscriber believes happened with
// nothing on either side to show for it.
//
// # Why reprocessing is safe even if it races the fast path
//
// The recovery poller does NOT trust anything captured at request time —
// it re-reads IsSuppressed/FindByEmail fresh (processIntakeRow, below) and
// dispatches through the exact same dispatchMutation the fast path uses
// (subscribe.go). Running that dispatch twice for the same signup is safe
// by construction, not by accident: Create's ErrEmailExists handling
// (newSignup) already covers "lost a race with a concurrent Create" (#0026),
// and ClaimAndEnqueueConfirmation/ClaimAndEnqueueAlreadySubscribed's atomic,
// cooldown-gated claims (#0126) already make a second attempt to send the
// same mail a safe no-op. So double-dispatch was already a property this
// package had to hold for ordinary concurrent requests (two tabs, a
// double-submit); the recovery poller merely exercises it from a second
// call site instead of introducing a new failure mode.
//
// intakeGraceDelay exists purely to make routine double-processing rare,
// not to make it safe (it already is): outbox.Item.Delay pushes a newly
// enqueued intake row's next_attempt_at into the future by this much, so
// ClaimDue's WHERE next_attempt_at <= now() excludes it until the fast,
// in-memory path (which normally finishes in low single-digit
// milliseconds — mutateJobTimeout bounds it at 10s) has had every
// reasonable chance to get there first and mark it done. The delay only
// matters for the crash/queue-full case this file exists to catch; under
// normal operation the recovery poller never sees a row at all, because
// markIntakeDone has already transitioned it out of 'queued' long before
// intakeGraceDelay elapses.
package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

const (
	// intakeGraceDelay is how long a freshly enqueued KindSubscribeIntake
	// row waits before it becomes eligible for the recovery poller to
	// claim — see the package doc comment above. Sized generously against
	// mutateJobTimeout (the fast path's own bound) rather than against
	// measured machine load (CLAUDE.md §5): the fast path must be
	// GENUINELY stuck or gone, not merely slow, before the recovery
	// poller's re-processing is worth the redundant work.
	intakeGraceDelay = 3 * mutateJobTimeout

	// intakeBatchSize mirrors mailing.outboxDefaultBatchSize — transactional
	// volume here is a handful of signups a minute (CLAUDE.md §5: no
	// performance requirement in this project), so this is generous
	// headroom, not a sizing exercise.
	intakeBatchSize = 20

	// intakePollInterval mirrors mailing.outboxDefaultPollInterval. A
	// KindSubscribeIntake row this poller ever actually claims already
	// represents an abnormal case (queue-full or a crash) — polling every
	// few seconds is more than fast enough for a durability backstop, not
	// a latency-sensitive path.
	intakePollInterval = 5 * time.Second

	// intakeRowTimeout bounds one claimed row's re-processing (the fresh
	// suppression/FindByEmail reads plus dispatchMutation), the same role
	// mutateJobTimeout plays for the fast path.
	intakeRowTimeout = mutateJobTimeout
)

// intakeOrphanStaleAfter bounds how long a claimed-but-unfinished
// KindSubscribeIntake row is treated as still legitimately in flight
// before OrphanSweep (#0122) releases it back to 'queued'. Releasing a row
// that is still genuinely being reprocessed is the #0254 failure mode: a
// sweep releases a live claim, and the recovery poller then reprocesses
// the row a second time while the first pass is still running on it.
//
// #0294: the original derivation, 2 * intakeRowTimeout, sized itself
// against ONE row's worst case. But intakePass claims a whole batch (up to
// intakeBatchSize rows) with ONE outbox.Store.ClaimDue call — the same
// batch-stamping mechanism #0284 found in internal/mailing's own worker —
// and intakePass then reprocesses that batch serially, one row at a time,
// so the last row's claim age when it finishes includes however long
// every row ahead of it took, not just its own bound.
//
// Verified before deriving anything (per this issue's own instruction,
// since #0295 found a sibling issue's identical-looking premise was
// false): subscribe_intake.go's poller uses outbox.Store.ClaimDue, the
// SAME batch-stamping method internal/mailing/outbox_worker.go's pass
// uses — not internal/mailing/worker.go's SendStore.ClaimRow, which
// stamps claimed_at per recipient and is why #0295 found no batching flaw
// there. So this file is #0284's family, confirmed by reading
// intakePass's own ClaimDue call below, not assumed from the issue title.
//
// Unlike #0284's fix, there is no rate limiter here at all to rule out as
// a term: intakePass's loop calls processIntakeRow back-to-back with
// nothing pacing it. Each row's ENTIRE reprocessing — the fresh
// IsSuppressed and FindByEmail reads, dispatchMutation, and the final
// MarkSent — runs inside one context.WithTimeout(h.sendCtx,
// intakeRowTimeout) (processIntakeRow, below), so intakeRowTimeout is
// both a row's failure ceiling AND, under normal operation, an upper
// bound on how long that row can occupy the loop before control passes to
// the next row. This bound charges every row in a full batch, tail row
// included, that full intakeRowTimeout:
//
//	intakeBatchSize * intakeRowTimeout = 20 * 10s = 200s
//
// for the batch's own row-by-row processing — plus one more
// intakeRowTimeout for intakePass's ClaimDue call itself (claimCtx
// below), which is where the batch's shared claimed_at is actually
// stamped. outbox.Store.ClaimDue's single `UPDATE ... RETURNING` sets
// claimed_at = now() near the start of that one statement (an implicit,
// single-statement transaction — now() is that transaction's start time),
// but the call does not return control to intakePass until the RETURNING
// rows have been scanned back over the wire: real elapsed time that
// happens BEFORE row 1's own intakeRowTimeout clock even starts, and that
// is charged to no row above.
//
// Unlike #0284's equivalent gap — outbox_worker.go's pass calls ClaimDue
// on ctx from `go outboxWorker.Run(context.Background())`, an entirely
// undeadlined context, so THAT gap is unbounded and #0284's own doc
// comment says explicitly no formula over its constants can ever close
// it — THIS gap is bounded: intakePass's ClaimDue call runs inside
// claimCtx, context.WithTimeout(h.sendCtx, intakeRowTimeout), so it
// cannot exceed intakeRowTimeout without the call itself failing and
// returning no rows (and therefore no batch) at all. A real but
// CLOSEABLE term, so it is charged here rather than left as an unclosed
// residual:
//
//	intakeOrphanStaleAfter = (intakeBatchSize + 1) * intakeRowTimeout
//	                       = 21 * 10s = 210s
//
// Even 210s is not a claim that context cancellation is instantaneous.
// It assumes pgx and the network underneath it honor ctx's deadline
// promptly enough that a stuck suppression/FindByEmail/dispatchMutation/
// MarkSent/ClaimDue call actually returns at or near intakeRowTimeout
// rather than materially later — a TCP read the OS cannot be made to
// abandon on cancellation is a residual no Go-level constant formula
// closes. That is the same CLASS of caveat #0284's final comment names
// for outboxOrphanStaleAfter (context.Background() there is a stronger,
// unconditional version of the same underlying assumption); not fixed
// here, and not this file's gap to close.
//
// Considered and rejected: re-stamping claimed_at per row — as
// internal/mailing/worker.go's SendStore.ClaimRow already does, per #0295
// — would make a per-row window honest and small instead of this
// batch-wide one. That is a change to internal/outbox's shared ClaimDue
// path, tracked separately (#0297), not built here. #0284 made the same
// call for outboxOrphanStaleAfter, for the same reason: this window being
// too LARGE only costs recovery latency on the crash path (h.sendCtx
// cancelling on a graceful Close lets intakePass finish or abandon its
// current batch in an orderly way; OrphanSweep exists for a hard kill),
// while too SMALL risks a duplicate mutation dispatch, #0254's failure
// mode. Choose too large.
//
// Expressed from the real package constants, not a hand-computed
// literal, so raising intakeBatchSize or intakeRowTimeout moves this
// window automatically. See
// TestIntakeOrphanStaleAfterCoversFullBatch
// (subscribe_intake_orphan_stale_after_test.go) for the invariant this
// maintains, checked against an independently expressed worst case.
//
// A var, not a const, so a test can shrink it; NOT sized against measured
// machine load (CLAUDE.md §5).
var intakeOrphanStaleAfter = time.Duration(intakeBatchSize+1) * intakeRowTimeout

// subscribeIntakePayload is a KindSubscribeIntake row's payload — the
// request-shape facts Subscribe learned synchronously (subscribe.go) and
// has no other durable home for. Deliberately does NOT include the
// suppressed/existing snapshot mutateJob carries for the fast path:
// processIntakeRow re-reads both fresh (see the package doc comment for
// why that is the safer choice for a row that may be reprocessed
// significantly later than the original request).
type subscribeIntakePayload struct {
	InterestIDs       []int64 `json:"interest_ids"`
	IsBot             bool    `json:"is_bot"`
	SignupIP          string  `json:"signup_ip"`
	SignupUserAgent   string  `json:"signup_user_agent"`
	UTMSource         string  `json:"utm_source"`
	UTMMedium         string  `json:"utm_medium"`
	UTMCampaign       string  `json:"utm_campaign"`
	ConfirmTTLSeconds int64   `json:"confirm_ttl_seconds"`
}

// runIntakeWorker polls outbound_queue for KindSubscribeIntake rows the
// fast path never finished, until h.sendCtx is cancelled (Close). Started
// once by NewSubscribeHandler when intake is non-nil; closes h.intakeDoneCh
// on return so Close can wait for it deterministically, the same shape
// runMutateWorker/mutateWG already establish for the mutation pool.
func (h *SubscribeHandler) runIntakeWorker() {
	defer close(h.intakeDoneCh)
	for {
		select {
		case <-h.sendCtx.Done():
			return
		default:
		}

		processed, err := h.intakePass()
		if err != nil {
			h.log.Error("subscribe: intake recovery pass failed", "err", err)
		}
		if processed {
			continue
		}

		select {
		case <-h.sendCtx.Done():
			return
		case <-time.After(intakePollInterval):
		}
	}
}

// intakePass sweeps orphaned claims, then claims and reprocesses one batch
// of due KindSubscribeIntake rows. Returns processed=true if it claimed at
// least one row, so runIntakeWorker can skip the poll wait — the same
// "don't busy-loop on an empty pass" discipline internal/mailing.
// OutboxWorker.pass already establishes (#0122).
func (h *SubscribeHandler) intakePass() (bool, error) {
	// #0254's review bounce: scoped to KindSubscribeIntake, the same kind
	// ClaimDue below is scoped to, so this sweep — which runs every 5s with
	// a 20s staleness window sized for THIS poller's own fast path — can
	// never release a live claim belonging to
	// internal/mailing.OutboxWorker, which legitimately holds a mail row
	// 'sending' for up to ~35s. An earlier, unfiltered version of this call
	// released a live confirmation-email claim mid-send, which led to that
	// message being sent a second time — see OrphanSweep's doc comment for
	// the full chain and
	// TestSubscribeIntakeWorker_OrphanSweepDoesNotTouchOtherKinds
	// (subscribe_intake_test.go) for the regression proof.
	sweepCtx, sweepCancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
	swept, err := h.intake.OrphanSweep(sweepCtx, intakeOrphanStaleAfter, outbox.KindSubscribeIntake)
	sweepCancel()
	if err != nil {
		return false, err
	}
	if swept > 0 {
		h.log.Warn("subscribe: intake orphan sweep reclaimed rows", "count", swept)
	}

	claimCtx, claimCancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
	rows, err := h.intake.ClaimDue(claimCtx, intakeBatchSize, outbox.KindSubscribeIntake)
	claimCancel()
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}

	for _, row := range rows {
		select {
		case <-h.sendCtx.Done():
			// Leave the rest of this batch claimed ('sending'); OrphanSweep
			// reclaims it once intakeOrphanStaleAfter passes, the same
			// shutdown allowance internal/mailing.OutboxWorker.pass makes.
			return true, nil
		default:
		}
		h.processIntakeRow(row)
	}
	return true, nil
}

// processIntakeRow re-derives the same two fixed reads Subscribe performed
// synchronously at request time — IsSuppressed and FindByEmail, both read
// FRESH rather than trusting anything from the original request, since this
// row may be reprocessed long after that request returned (see the package
// doc comment) — then runs it through dispatchMutation, the exact dispatch
// the fast path uses. Marks the row done via MarkSent (existing method: the
// row is already 'sending', this call's own ClaimDue having put it there)
// regardless of dispatchMutation's outcome, matching markIntakeDone's own
// "processed, not necessarily succeeded" convention — see that method's doc
// comment for why.
func (h *SubscribeHandler) processIntakeRow(row outbox.Row) {
	ctx, cancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
	defer cancel()

	var payload subscribeIntakePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		h.log.Error("subscribe: intake row payload unmarshal failed", "id", row.ID, "err", err)
		// A row whose payload cannot be unmarshalled will never succeed on
		// retry either — MarkRetryOrAbandon (not MarkSent) so it eventually
		// reaches the terminal 'abandoned' state via the ordinary backoff
		// schedule rather than looping forever, mirroring how
		// internal/mailing.OutboxWorker.sendOne treats a render failure.
		if _, err2 := h.intake.MarkRetryOrAbandon(ctx, row.ID, row.Attempts, err.Error(), outbox.DefaultMaxRetries); err2 != nil {
			h.log.Error("subscribe: marking unparsable intake row failed", "id", row.ID, "err", err2)
		}
		return
	}

	now := h.now()
	suppressed, suppErr := h.suppression.IsSuppressed(ctx, row.Recipient)
	existing, findErr := h.subs.FindByEmail(ctx, row.Recipient)
	evidence := subscribers.RestartSignupInput{
		SignupIP:        payload.SignupIP,
		SignupUserAgent: payload.SignupUserAgent,
		UTMSource:       payload.UTMSource,
		UTMMedium:       payload.UTMMedium,
		UTMCampaign:     payload.UTMCampaign,
		ConfirmTTL:      time.Duration(payload.ConfirmTTLSeconds) * time.Second,
	}

	h.dispatchMutation(ctx, payload.IsBot, row.Recipient, payload.InterestIDs, evidence, now,
		suppressed, suppErr, existing, findErr)

	if _, err := h.intake.MarkSent(ctx, row.ID, ""); err != nil {
		h.log.Error("subscribe: marking recovered intake row processed failed", "id", row.ID, "err", err)
	}
}

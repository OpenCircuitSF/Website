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
// SelectDue's WHERE next_attempt_at <= now() excludes it until the fast,
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
// # #0297 — collapsed to a per-row bound
//
// Through #0294, this window was derived from a whole intakeBatchSize
// batch (210s), because intakePass claimed the batch atomically with ONE
// outbox.Store.ClaimDue call and then reprocessed it serially — so the
// LAST row's claim age included every row ahead of it, not just its own
// bound.
//
// #0297 changed intakePass, below, to claim per row instead: SelectDue
// selects the batch's ids WITHOUT claiming anything, and ClaimRow claims
// exactly one row, individually, immediately before THAT row's own
// reprocessing begins — the same select-then-per-row-claim shape
// internal/mailing.SendStore's ClaimBatch/ClaimRow always had (#0295) and
// internal/mailing.OutboxWorker.pass was also moved onto (#0297). A row
// waiting its turn in intakePass's loop is still plain 'queued' until
// ClaimRow reaches it, so batch size drops out of this bound entirely —
// there is only ever, at most, ONE row 'sending' under this poller at a
// time.
//
// The single-row bound: intakePass's own ClaimRow call is wrapped in a
// bounded context.WithTimeout(h.sendCtx, intakeRowTimeout) (claimCtx,
// below) — deliberately bounded FROM h.sendCtx rather than detached from
// it the way, e.g., internal/mailing.OutboxWorker.sendOne's terminal
// writes are detached from their caller: a claim that cannot complete
// before this handler's own shutdown should not be taken at all, not
// raced to completion after h.sendCtx is already cancelled. If that round
// trip alone hangs, up to intakeRowTimeout elapses after claimed_at is
// committed server-side before this process even learns the claim
// succeeded, which is real elapsed claim age. Then processIntakeRow's
// entire reprocessing — the fresh IsSuppressed and FindByEmail reads,
// dispatchMutation, and the final MarkSent — runs inside its own
// context.WithTimeout(h.sendCtx, intakeRowTimeout). Worst case, both
// terms:
//
//	2 * intakeRowTimeout = 2 * 10s = 20s
//
// # #0297's review — zero margin, corrected
//
// This fix's first cut set intakeOrphanStaleAfter to exactly
// 2 * intakeRowTimeout — the derived worst case above, with no slack —
// unlike outboxOrphanStaleAfter and worker.go's own orphanStaleAfter,
// which both carry real margin over their own worst cases (30s and 35s
// respectively; see outboxOrphanStaleAfter's own doc comment,
// mailing/outbox_worker.go). intakeOrphanStaleAfter now carries the same
// margin-per-term posture: one additional intakeRowTimeout on top of the
// two worst-case terms above.
//
//	3 * intakeRowTimeout = 3 * 10s = 30s
//
// 30s covers the 20s worst case with a full intakeRowTimeout (10s) of
// real margin — the same "one extra term of margin" shape the other two
// windows reduce to once expressed in their own constants.
//
// Even with that margin, this is not a claim that context cancellation is
// instantaneous. It assumes pgx and the network underneath it honor ctx's
// deadline promptly enough that a stuck ClaimRow/suppression/FindByEmail/
// dispatchMutation/MarkSent call actually returns at or near
// intakeRowTimeout rather than materially later — a TCP read the OS
// cannot be made to abandon on cancellation is a residual no Go-level
// constant formula closes. Same CLASS of caveat #0284's and #0294's final
// comments name for their own windows; not fixed here.
//
// Expressed from the real package constant, not a hand-computed literal,
// so raising intakeRowTimeout moves this window automatically —
// intakeBatchSize is deliberately NOT a term any more. See
// TestIntakeOrphanStaleAfterCoversSingleRowHold
// (subscribe_intake_orphan_stale_after_test.go) for the invariant this
// maintains — the derived 2 * intakeRowTimeout worst case, not the 10s
// single-term figure an earlier version of that test checked.
//
// A var, not a const, so a test can shrink it; NOT sized against measured
// machine load (CLAUDE.md §5).
var intakeOrphanStaleAfter = 3 * intakeRowTimeout

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

// intakePass sweeps orphaned claims, then selects and reprocesses one batch
// of due KindSubscribeIntake rows — claiming and reprocessing each row
// individually (#0297), not the whole batch at once. Returns processed=true
// if it claimed at least one row, so runIntakeWorker can skip the poll
// wait — the same "don't busy-loop on an empty pass" discipline
// internal/mailing.OutboxWorker.pass already establishes (#0122).
func (h *SubscribeHandler) intakePass() (bool, error) {
	// #0254's review bounce: scoped to KindSubscribeIntake, the same kind
	// SelectDue below is scoped to, so this sweep — which runs every 5s with
	// a 20s staleness window sized for THIS poller's own fast path — can
	// never release a live claim belonging to
	// internal/mailing.OutboxWorker, which legitimately holds a mail row
	// 'sending' for up to ~70s. An earlier, unfiltered version of this call
	// released a live confirmation-email claim mid-send, which led to that
	// message being sent a second time — see OrphanSweep's doc comment for
	// the full chain and
	// TestSubscribeIntakeWorker_OrphanSweepDoesNotTouchOtherKinds
	// (subscribe_intake_test.go) for the regression proof.
	sweepCtx, sweepCancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
	swept, err := h.intake.OrphanSweep(sweepCtx, intakeOrphanStaleAfter, []outbox.Kind{outbox.KindSubscribeIntake})
	sweepCancel()
	if err != nil {
		return false, err
	}
	if swept > 0 {
		h.log.Warn("subscribe: intake orphan sweep reclaimed rows", "count", swept)
	}

	// #0297: SelectDue only SELECTs — it claims nothing. The atomic claim
	// (ClaimRow) happens per id, below, immediately before that row's own
	// reprocessing begins, so claimed_at reflects when THIS row's own work
	// started rather than an earlier batch-wide stamp.
	selectCtx, selectCancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
	ids, err := h.intake.SelectDue(selectCtx, intakeBatchSize, []outbox.Kind{outbox.KindSubscribeIntake})
	selectCancel()
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, nil
	}

	processed := false
	for _, id := range ids {
		select {
		case <-h.sendCtx.Done():
			// Every id not yet reached is still plain 'queued' (SelectDue
			// claimed nothing) — nothing to leave for the orphan sweep;
			// only a row genuinely claimed below is ever 'sending', and
			// processIntakeRow always runs it to completion before this
			// loop can check h.sendCtx.Done() again.
			return processed, nil
		default:
		}

		claimCtx, claimCancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
		row, claimed, err := h.intake.ClaimRow(claimCtx, id)
		claimCancel()
		if err != nil {
			h.log.Error("subscribe: claiming intake row failed", "id", id, "err", err)
			continue
		}
		if !claimed {
			// Lost the race — a concurrent poller claimed it first, or an
			// orphan sweep already reclaimed it while it waited its turn
			// in this batch. Nothing to do; the row is exactly where it
			// should be.
			continue
		}
		processed = true
		h.processIntakeRow(row)
	}
	return processed, nil
}

// processIntakeRow re-derives the same two fixed reads Subscribe performed
// synchronously at request time — IsSuppressed and FindByEmail, both read
// FRESH rather than trusting anything from the original request, since this
// row may be reprocessed long after that request returned (see the package
// doc comment) — then runs it through dispatchMutation, the exact dispatch
// the fast path uses. Marks the row done via MarkSent (existing method: the
// row is already 'sending', this call's own ClaimRow — intakePass, above —
// having put it there) regardless of dispatchMutation's outcome, matching
// markIntakeDone's own
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

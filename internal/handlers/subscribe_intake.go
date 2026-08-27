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

	// intakeOrphanStaleAfter mirrors internal/mailing's
	// outboxOrphanStaleAfter derivation shape (a small multiple of the
	// worker's own per-row bound) — sized against intakeRowTimeout, not
	// against measured load (CLAUDE.md §5). A row this poller claims but
	// never finishes (this process killed mid-batch) is released back to
	// 'queued' by OrphanSweep once claimed_at is this stale, the same
	// safety net #0122 established for internal/mailing.SendStore and
	// #0126 copied for internal/outbox generally.
	intakeOrphanStaleAfter = 2 * intakeRowTimeout
)

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
	sweepCtx, sweepCancel := context.WithTimeout(h.sendCtx, intakeRowTimeout)
	swept, err := h.intake.OrphanSweep(sweepCtx, intakeOrphanStaleAfter)
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

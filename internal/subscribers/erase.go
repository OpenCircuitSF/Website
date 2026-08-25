// erase.go implements GDPR/CCPA erasure (PRD §11; #0060) as one method on
// *Store: a hard delete of the subscribers row that leaves behind exactly
// three of the four things web/src/views/PrivacyPolicy.svelte's "How to
// leave" section promises it will (the fourth, an audit_log entry, is
// written by the HANDLER after Erase returns — see
// internal/handlers/admin_subscribers.go's audit.Entry{Action:
// ActionSubscriberErased} call — not by this method, which has no
// dependency on internal/audit) —
//
//  1. a permanent `manual`-reason suppressions row, so the address cannot be
//     silently re-added by a future import or signup;
//  2. anonymized (not deleted) email_sends rows, so historical campaign
//     counts never silently change;
//  3. email_events rows, untouched — the raw SES/SNS payload stays for
//     deliverability forensics, but with no way back to this person once
//     the subscribers row and the email_sends snapshot are both gone.
//
// This comment used to say "the three things" as if that were the whole
// list — #0226 corrected it after finding it stale against the privacy
// policy's own four-item list (the fourth landed in #0060's own Phase 3
// bounce, months before this comment was last touched). See
// web/src/views/PrivacyPolicy.guard.test.ts for the guard tying the
// policy's list to this package's and admin_subscribers.go's actual
// behavior, and PrivacyPolicy.svelte's own header comment for the fourth
// item's exact wording.
//
// # Why hard-deleting the subscriber does not defeat the "complained never
// # auto-resubscribes" rule (CLAUDE.md §9)
//
// That rule exists to stop a suppressed address from silently returning to
// the list. Erasure's whole POINT is a permanent block on return, so the two
// are aligned, not in tension, PROVIDED the block outlives the row — which
// is exactly what suppressions is for (see suppressions.go's package doc
// comment: "keyed by NORMALIZED EMAIL... deliberately no foreign key to
// subscribers... survive a subscriber's hard deletion (#0060)"). Erase
// always adds a `manual` suppression row before deleting the subscribers
// row, on top of whatever the address may already carry (a `complaint` row
// from #0038's SES ingestion survives untouched, reason-scoped, if one
// exists) — so an address that was complained when it was erased is not
// merely as blocked as before; it now has TWO independent, orphan-safe
// reasons blocking a future resignup. See ErasureBlocksResubscribe (in the
// test file) for the executed proof.
//
// # Why email_sends is anonymized rather than left to migration 000017's old
// # ON DELETE CASCADE
//
// It used to cascade-delete. #0060 widened email_sends.subscriber_id to
// nullable with ON DELETE SET NULL (migrations/000017, edited in place —
// greenfield, CLAUDE.md §1) specifically so this method could anonymize
// those rows instead of losing them: Erase overwrites `email` for every
// email_sends row still pointing at this subscriber BEFORE deleting the
// subscribers row, then the DELETE's cascade sets subscriber_id to NULL for
// free. Anonymizing first (not relying on the FK alone) matters because the
// FK only decouples the id column — it does nothing about the `email`
// snapshot, which is the actual PII #0059's export and #0049's stats screen
// would otherwise keep showing.
//
// # Why a pending or in-flight send blocks erasure
//
// internal/mailing.AudienceStore.RecheckEligibleTx (audience.go) INNER JOINs
// email_sends to subscribers on subscriber_id immediately before a send:
// `FROM email_sends es JOIN subscribers s ON s.id = es.subscriber_id`. A row
// whose subscriber_id has gone NULL (or whose subscribers row is simply
// gone) matches neither side of that join, so it would never come back in
// either RecheckEligibleTx's eligible or skipped slice — and MarkSkippedTx
// is only ever called on rows RecheckEligibleTx returns. That row would sit
// at status='queued' forever: not sent (never eligible), not skipped (never
// reported), a silent stuck ghost the worker can't clear. Erase refuses
// (ErrHasPendingSends) while any email_sends row for this subscriber is
// 'queued' or 'sending', rather than reaching into internal/mailing to fix
// RecheckEligibleTx's join — that fix belongs to whichever issue next
// touches internal/mailing/audience.go, and is reported, not made here (see
// CLAUDE.md §9 and this issue's dispatch). An operator hitting this waits
// for the campaign to finish (sent/failed/skipped) or cancels it first.
package subscribers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// erasedEventPlaceholder builds the subscriber_events email placeholder for
// a given subscriber id — see Erase's redaction comment for why this is
// keyed by the SUBSCRIBER id (unlike the per-row email_sends placeholder
// three lines above it in Erase).
func erasedEventPlaceholder(subscriberID int64) string {
	return fmt.Sprintf("erased-%d@erased.invalid", subscriberID)
}

// ErrHasPendingSends is returned by Erase when the subscriber has at least
// one email_sends row still 'queued' or 'sending' — see the package doc
// comment's "in-flight send" section for why erasing through that would
// leave the row stuck rather than actually protecting anyone.
var ErrHasPendingSends = errors.New("subscribers: cannot erase a subscriber with a queued or in-progress campaign delivery")

// ErasureResult is what Erase did, for the caller's audit entry and admin
// response. It is the only place this information survives — by the time
// Erase returns, the subscribers row and its subscriber_interests rows are
// both gone.
type ErasureResult struct {
	ID                    int64
	Email                 string
	PreviousStatus        string
	InterestsRemoved      int
	EmailSendsAnonymized  int64
	SuppressionAdded      bool
	SuppressionPreexisted bool
}

// Erase permanently deletes the subscribers row for id (cascading its
// subscriber_interests rows via migrations/000010's ON DELETE CASCADE),
// after anonymizing any email_sends rows referencing it and adding a
// `manual`-reason suppressions row for its address. See the package doc
// comment for why each of those three things happens, and in this order.
//
// Runs as a single transaction with SELECT ... FOR UPDATE locking the
// subscribers row first, so a concurrent mutation (an unsubscribe, a status
// change from an SES event) can't interleave with an erasure already in
// flight and leave a half-erased state.
//
// Returns ErrNotFound if no subscribers row matches id, ErrHasPendingSends
// if the subscriber has an in-flight send (see above) — both checked before
// anything is mutated.
func (s *Store) Erase(ctx context.Context, id int64, now time.Time) (ErasureResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: beginning erase tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var email, status string
	err = tx.QueryRow(ctx,
		`SELECT email, status FROM subscribers WHERE id = $1 FOR UPDATE`, id,
	).Scan(&email, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErasureResult{}, ErrNotFound
	case err != nil:
		return ErasureResult{}, fmt.Errorf("subscribers: locking subscriber %d for erasure: %w", id, err)
	}

	// Refuse while a send is in flight for this subscriber — see the
	// package doc comment's "in-flight send" section. Checked inside the
	// same transaction/lock as everything else so a send cannot be
	// materialized against this subscriber between this check and the
	// DELETE below.
	var pending bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM email_sends
		     WHERE subscriber_id = $1 AND status IN ('queued', 'sending')
		 )`, id,
	).Scan(&pending); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: checking pending sends for %d: %w", id, err)
	}
	if pending {
		return ErasureResult{}, ErrHasPendingSends
	}

	// Same refusal, extended to outbound_queue (#0126) — but scoped to
	// 'sending' only, DELIBERATELY NARROWER than the email_sends check
	// above (which also blocks on 'queued'). outbound_queue's
	// subscriber_id is ON DELETE CASCADE, so without this check the
	// DELETE below could silently remove a row a worker has actually
	// CLAIMED and is mid-send on ('sending') — the same "don't destroy a
	// live in-flight operation" concern the email_sends check exists for.
	// A merely 'queued'-but-unclaimed confirmation is different: nothing
	// is in flight, and the row exists only because a normal signup
	// enqueues one (since #0126, EVERY non-synthetic Create leaves one
	// queued until the worker sends it). Blocking erasure on that would
	// make ordinary GDPR erasure of a subscriber who hasn't yet had their
	// confirmation delivered impossible until the worker catches up —
	// verified experimentally while writing this issue's tests: every
	// TestErase_* test in this package failed with ErrHasPendingSends the
	// moment the check also covered 'queued', because every Create() in
	// their setup leaves exactly such a row. Blocking only 'sending'
	// preserves the actual property #0126's plan cites ("a row a worker
	// has claimed and is mid-send on") without that regression. Flagged
	// for the reviewer: the plan's literal text said to mirror
	// email_sends' IN ('queued', 'sending') exactly; this departs from
	// that on the grounds above.
	var pendingOutbound bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM outbound_queue
		     WHERE subscriber_id = $1 AND status = 'sending'
		 )`, id,
	).Scan(&pendingOutbound); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: checking pending outbound queue rows for %d: %w", id, err)
	}
	if pendingOutbound {
		return ErasureResult{}, ErrHasPendingSends
	}

	// Count what subscriber_interests is about to lose to the CASCADE, so
	// the caller's audit row can say how many without a second round trip
	// after the DELETE (by which point the rows, and therefore the count,
	// are already gone).
	var interestsRemoved int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM subscriber_interests WHERE subscriber_id = $1`, id,
	).Scan(&interestsRemoved); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: counting interests for %d: %w", id, err)
	}

	// Anonymize email_sends BEFORE the delete: migrations/000017's FK only
	// decouples subscriber_id (via ON DELETE SET NULL, #0060) — it does
	// nothing about the `email` snapshot column, which is the actual PII.
	// The placeholder is derived from the send row's own id, not the
	// subscriber's, so it stays unique per row with no correlation an
	// export or stats screen could use to re-identify the person from two
	// erased subscribers' rows alone.
	anonTag, err := tx.Exec(ctx,
		`UPDATE email_sends
		    SET email = 'erased-' || id || '@erased.invalid'
		  WHERE subscriber_id = $1`, id,
	)
	if err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: anonymizing email_sends for %d: %w", id, err)
	}

	// Permanent suppression (reason=manual), same idempotent upsert Suppress
	// uses (addSuppression, suppressions.go) — a repeat erasure attempt, or
	// an address that already carries a `complaint`/`hard_bounce` row, adds
	// a second independent reason rather than erroring or duplicating one
	// that already exists. Runs inside this transaction (AddTx's whole
	// reason to exist, per suppressions.go's package doc comment): the
	// suppression and the delete below commit or roll back together, so a
	// crash between them can never leave a subscriber genuinely deleted
	// with no block on their return.
	//
	// Checked for pre-existence BEFORE the upsert (rather than comparing the
	// upsert's returned created_at against now, which addSuppression's own
	// no-op ON CONFLICT UPDATE would make a fragile clock-ordering
	// assumption) so SuppressionPreexisted reports whether THIS exact
	// (email, manual) row already blocked the address before this call.
	var suppressionPreexisted bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM suppressions WHERE email = lower(trim($1)) AND reason = $2
		 )`, email, SuppressionReasonManual,
	).Scan(&suppressionPreexisted); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: checking existing suppression for %d: %w", id, err)
	}
	if _, err := addSuppression(ctx, tx, NewSuppression{
		Email:  email,
		Reason: SuppressionReasonManual,
		Note:   "GDPR/CCPA erasure request",
	}, now); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: adding erasure suppression for %d: %w", id, err)
	}

	// Redact subscriber_events BEFORE the DELETE (#0126): the address's
	// history must survive erasure as the evidence the erasure was
	// performed, per PRD §6.11 and this package's events.go doc comment.
	// subscriber_id is nulled for free by the DELETE below via the
	// table's ON DELETE SET NULL FK — this UPDATE only needs to touch
	// email.
	//
	// The placeholder is derived from the SUBSCRIBER's id, not each row's
	// own id — deliberately different from the email_sends redaction three
	// lines above, whose placeholder is per-row so two erased subscribers'
	// email_sends rows can never be correlated. Here the point is the
	// opposite: once subscriber_id is NULL, the placeholder is the only
	// key left, and an unlinked pile of rows would not be evidence that an
	// erasure was performed for a specific address — which is the whole
	// reason this table's rows survive at all. Keying by subscriber id
	// keeps the address's own history grouped without leaking anything: a
	// subscriber id is not personal data, and the row it pointed at is
	// gone by the time anyone could read this placeholder.
	//
	// WIDENED (#0126 phase-3 review, defect 1): "WHERE subscriber_id = $1"
	// alone missed every subscriber_events row suppressions.go writes with
	// SubscriberID left nil — ActionSuppressed (suppressions.go's
	// addSuppression) and ActionUnsuppressed (Remove) are both keyed by
	// email, not subscriber id, by that package's own design (see its
	// doc comment). Erase itself creates exactly one of these seconds
	// earlier, via the addSuppression call directly above: the `manual`
	// suppression this method always adds writes a `suppressed` event
	// carrying the real address with subscriber_id NULL, and the old
	// predicate never matched it — proved by execution in the review
	// (Create -> Erase left one row still holding the real address). Any
	// PRE-EXISTING suppressed/unsuppressed row for this address (a prior
	// hard bounce, complaint, or admin action) leaked the same way. The
	// second predicate closes both: `email` is compared against the
	// SAME lower(trim(...)) normalization every write in this package
	// applies, matching the value `email` above was already read as
	// (subscribers.email is itself stored lower(trim(...)) at INSERT —
	// see store.go's Create doc comment) — so this is defense in depth,
	// not a behavior change for the common case.
	if _, err := tx.Exec(ctx,
		`UPDATE subscriber_events SET email = $2 WHERE subscriber_id = $1 OR email = lower(trim($3))`,
		id, erasedEventPlaceholder(id), email,
	); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: redacting subscriber_events for %d: %w", id, err)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, id)
	if err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: deleting subscriber %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		// Raced with a concurrent delete between the SELECT ... FOR UPDATE
		// above and here — vanishingly unlikely (the row was locked), but
		// report it as not-found rather than a silently empty success.
		return ErasureResult{}, ErrNotFound
	}

	// Written AFTER the redaction above, with the same placeholder address,
	// so the erasure's own evidence does not reintroduce the real address
	// into a table this method just finished scrubbing it from.
	// subscriber_id is already NULL by this point (the DELETE's cascade
	// ran before this INSERT), matching every other post-erasure row.
	if err := RecordEventTx(ctx, tx, Event{
		Email:  erasedEventPlaceholder(id),
		Action: ActionErased,
	}); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: recording erased event for %d: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ErasureResult{}, fmt.Errorf("subscribers: committing erase tx for %d: %w", id, err)
	}

	return ErasureResult{
		ID:                    id,
		Email:                 email,
		PreviousStatus:        status,
		InterestsRemoved:      interestsRemoved,
		EmailSendsAnonymized:  anonTag.RowsAffected(),
		SuppressionAdded:      true,
		SuppressionPreexisted: suppressionPreexisted,
	}, nil
}

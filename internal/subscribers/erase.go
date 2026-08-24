// erase.go implements GDPR/CCPA erasure (PRD §11; #0060) as one method on
// *Store: a hard delete of the subscribers row that leaves behind exactly
// the three things #0075's privacy policy already promises it will —
//
//  1. a permanent `manual`-reason suppressions row, so the address cannot be
//     silently re-added by a future import or signup;
//  2. anonymized (not deleted) email_sends rows, so historical campaign
//     counts never silently change;
//  3. email_events rows, untouched — the raw SES/SNS payload stays for
//     deliverability forensics, but with no way back to this person once
//     the subscribers row and the email_sends snapshot are both gone.
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

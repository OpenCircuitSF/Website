// pending.go adds the admin-facing pending-subscriber screen's data access
// (#0128, PRD §5.2/§6.3): listing addresses that signed up but never
// confirmed, resending a fresh confirmation, and sweeping expired ones.
//
// # Why this pairs with #0126
//
// Before #0126's durable outbound_queue, "why didn't they confirm?" had no
// answer — the send happened on a goroutine that left no trace. ListPending
// itself only reads the subscribers table; the per-address queue state an
// operator actually needs (queued / sent / abandoned) is joined by the
// handler layer (internal/handlers/admin_pending.go) against
// internal/outbox.Store.LatestByRecipients, not duplicated here.
package subscribers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// ErrPendingSubscriberNotFound is returned by AdminResendConfirmation when
// no non-synthetic subscribers row matches id.
var ErrPendingSubscriberNotFound = errors.New("subscribers: pending subscriber not found")

// ErrNotPending is returned by AdminResendConfirmation when the subscriber
// exists but is not currently status='pending' — nothing to resend to (they
// already confirmed, unsubscribed, bounced, or complained).
var ErrNotPending = errors.New("subscribers: subscriber is not pending")

// ErrResendCooldownActive is returned by AdminResendConfirmation when the
// last send (confirm_sent_at) is more recent than now-cooldown — the
// mail-bomb guard #0128's criteria require ("Resend is rate-limited per
// subscriber"). Not an error the caller logs as a failure; it means "wait
// and try again."
var ErrResendCooldownActive = errors.New("subscribers: resend cooldown active")

// ErrResendSuppressed is returned by AdminResendConfirmation when the
// address has a suppressions row — #0128's criterion "resending to a
// suppressed address is refused with a clear reason."
var ErrResendSuppressed = errors.New("subscribers: address is suppressed")

// ErrResendNotForInvited is returned by AdminResendConfirmation for a row
// this package's own Invited test (import_id set, consent_basis still
// NULL) identifies as an unaccepted import invitation (#0129). This method
// mints a NEW outbox.KindConfirmation row — the generic "confirm your
// subscription" template, which carries none of PRD §6.10.1's mandatory
// provenance sentence ("not optional copy") — so resending it onto an
// invited row would replace the person's only copy of WHY they were
// emailed with one that no longer says. The correct resend for an invited
// row is a fresh outbox.KindImportInvite, which this method does not build;
// refusing loudly here is preferred over silently sending the wrong
// template.
var ErrResendNotForInvited = errors.New("subscribers: cannot resend a generic confirmation to an import invitation")

// ListPending returns every non-synthetic status='pending' subscriber,
// ordered by confirm_sent_at — oldestFirst=true (the default the admin
// screen opens to, per #0128's criterion) sorts ascending (oldest wait
// first), false sorts descending. synthetic=true rows (#0046's per-admin
// test-send recipients, migration 000019) are excluded unconditionally, the
// same convention StatusCounts/List already establish for this table.
func (s *Store) ListPending(ctx context.Context, oldestFirst bool) ([]Subscriber, error) {
	order := "confirm_sent_at ASC NULLS LAST, id ASC"
	if !oldestFirst {
		order = "confirm_sent_at DESC NULLS LAST, id DESC"
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+subscriberColumns+`
		   FROM subscribers
		  WHERE status = $1 AND synthetic = false
		  ORDER BY `+order,
		StatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("subscribers: listing pending: %w", err)
	}
	defer rows.Close()

	var out []Subscriber
	for rows.Next() {
		sub, err := scanSubscriber(rows)
		if err != nil {
			return nil, fmt.Errorf("subscribers: scanning pending row: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating pending rows: %w", err)
	}
	return out, nil
}

// ResendResult is AdminResendConfirmation's success return: the subscriber
// row after the resend (fresh token, extended expiry, stamped
// confirm_sent_at) and the PREVIOUS confirm_sent_at (nil for a subscriber
// who never had a send claimed — the synthetic-skip path aside, this should
// not happen for a real signup, but the type allows it), for the caller's
// audit metadata.
type ResendResult struct {
	Subscriber            Subscriber
	PreviousConfirmSentAt *time.Time
}

// AdminResendConfirmation mints a fresh confirm_token, extends
// confirm_expires_at to now+ttl, stamps confirm_sent_at, and enqueues a new
// confirmation email — all inside one transaction, so a committed resend
// can never leave the new token unqueued (the same #0126 property
// ClaimAndEnqueueConfirmation and Create already establish for the public
// paths). Unlike ClaimAndEnqueueConfirmation (which reuses the subscriber's
// EXISTING token — the public "resend" is really "resend the same link"),
// this mints a genuinely NEW token: #0128's criterion is explicit ("mints a
// fresh token"), and an admin-triggered resend is a good moment to also
// invalidate whatever the previous link was, in case it leaked.
//
// Guarded, in order, inside the transaction (the row is locked with
// SELECT ... FOR UPDATE below so a concurrent resend or confirm can't race
// this check — see that query's own comment, and #0263 item 1, for why this
// doc comment used to credit the wrong mechanism):
//
//  1. the subscriber exists and is not synthetic (ErrPendingSubscriberNotFound)
//  2. status is 'pending' (ErrNotPending) — nothing to resend to otherwise
//  3. the row is not an unaccepted import invitation (ErrResendNotForInvited,
//     #0129) — see that error's own doc comment for why resending the
//     generic confirmation template onto one would be actively wrong
//  4. the address is not suppressed (ErrResendSuppressed)
//  5. confirm_sent_at is NULL or older than now-cooldown (ErrResendCooldownActive)
//     — checked in Go, against the row FOR UPDATE already locked above, NOT
//     via a claim-in-the-WHERE-clause UPDATE the way the public
//     ClaimAndEnqueueConfirmation enforces its own cooldown. The safety
//     property is the same (a second concurrent admin click can't both
//     win) but the mechanism differs: here it is the row lock that
//     serializes concurrent callers, not an atomic conditional UPDATE.
//     Mutation-proven under eight concurrent calls (#0128's phase-3
//     review): wins=1 cooldowns=7 queued_delta=1.
//
// A cooldown/suppression refusal is a normal outcome, not a system error —
// callers should not log it as one.
func (s *Store) AdminResendConfirmation(ctx context.Context, id int64, now time.Time, cooldown, ttl time.Duration) (ResendResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResendResult{}, fmt.Errorf("subscribers: beginning admin-resend tx for %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE is load-bearing, not decoration (#0263 item 1): the
	// cooldown check below (guard 4) is an ordinary Go `if`, not a claim
	// baked into an UPDATE's WHERE clause — this row lock is the ONLY thing
	// serializing two concurrent AdminResendConfirmation calls against the
	// same subscriber. Removing it turns the cooldown check into a
	// read-then-write race: two concurrent callers could both read
	// confirm_sent_at as "outside the cooldown" before either writes,
	// producing two resends instead of one. Do not remove it as apparently
	// redundant with the WHERE clause below — it is not redundant.
	row := tx.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE id = $1 AND synthetic = false FOR UPDATE`, id)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ResendResult{}, ErrPendingSubscriberNotFound
	case err != nil:
		return ResendResult{}, fmt.Errorf("subscribers: locking subscriber %d for admin resend: %w", id, err)
	}
	if sub.Status != StatusPending {
		return ResendResult{}, ErrNotPending
	}
	if sub.ImportID != nil && sub.ConsentBasis == nil {
		return ResendResult{}, ErrResendNotForInvited
	}

	var suppressed bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM suppressions WHERE email = $1)`, sub.Email,
	).Scan(&suppressed); err != nil {
		return ResendResult{}, fmt.Errorf("subscribers: checking suppression for %d: %w", id, err)
	}
	if suppressed {
		return ResendResult{}, ErrResendSuppressed
	}

	if sub.ConfirmSentAt != nil && sub.ConfirmSentAt.After(now.Add(-cooldown)) {
		return ResendResult{}, ErrResendCooldownActive
	}

	newTok, err := newToken()
	if err != nil {
		return ResendResult{}, err
	}
	confirmExpiresAt := now.Add(ttl)

	row = tx.QueryRow(ctx,
		`UPDATE subscribers
		    SET confirm_token = $2, confirm_expires_at = $3, confirm_sent_at = $4, updated_at = $4
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, newTok, confirmExpiresAt, now,
	)
	updated, err := scanSubscriber(row)
	if err != nil {
		return ResendResult{}, fmt.Errorf("subscribers: stamping admin resend for %d: %w", id, err)
	}

	if _, err := s.outbox.EnqueueTx(ctx, tx, outbox.Item{
		Kind:         outbox.KindConfirmation,
		Recipient:    updated.Email,
		SubscriberID: &updated.ID,
		Payload: confirmationPayload{
			ConfirmToken: newTok,
			ManageToken:  updated.ManageToken,
			TTLSeconds:   int64(ttl.Seconds()),
		},
	}); err != nil {
		return ResendResult{}, fmt.Errorf("subscribers: enqueueing admin resend for %d: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ResendResult{}, fmt.Errorf("subscribers: committing admin resend tx for %d: %w", id, err)
	}
	return ResendResult{Subscriber: updated, PreviousConfirmSentAt: sub.ConfirmSentAt}, nil
}

// ExpirePendingSweep clears confirm_token (and only confirm_token — the row
// is left status='pending', per #0128's criterion "the row left pending
// rather than deleted") for every non-synthetic pending subscriber whose
// confirm_expires_at has passed, and records one subscriber_events row per
// address: ActionConfirmationExpired for an ordinary website signup, or
// ActionInviteExpired (#0129) for a row still carrying an unaccepted import
// invitation — import_id set and consent_basis still NULL, the same
// row-state test Confirm and Unsubscribe use elsewhere in this package to
// recognize an invite without trusting caller intent. Either way the row is
// left exactly 'pending' — #0129's criterion "leaving the row pending,
// never re-mailing" is satisfied by construction: nothing in this method or
// anywhere else re-stamps invited_at or re-enqueues an invitation once
// invited_at is set (see ImportStore.Commit's doc comment on why that
// column is write-once). Returns the number of rows swept. Idempotent: a
// row with confirm_token already NULL never matches the WHERE clause again,
// so a repeated sweep (the caller's own poll loop) never double-writes the
// event for the same expiry.
func (s *Store) ExpirePendingSweep(ctx context.Context, now time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("subscribers: beginning expire-pending sweep tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`UPDATE subscribers
		    SET confirm_token = NULL, updated_at = $1
		  WHERE status = $2 AND synthetic = false
		    AND confirm_token IS NOT NULL
		    AND confirm_expires_at IS NOT NULL AND confirm_expires_at < $1
		 RETURNING id, email, import_id, consent_basis`,
		now, StatusPending,
	)
	if err != nil {
		return 0, fmt.Errorf("subscribers: sweeping expired pending signups: %w", err)
	}
	type expiredRow struct {
		id           int64
		email        string
		importID     *int64
		consentBasis *string
	}
	var expired []expiredRow
	for rows.Next() {
		var r expiredRow
		if err := rows.Scan(&r.id, &r.email, &r.importID, &r.consentBasis); err != nil {
			rows.Close()
			return 0, fmt.Errorf("subscribers: scanning expired pending row: %w", err)
		}
		expired = append(expired, r)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return 0, fmt.Errorf("subscribers: iterating expired pending rows: %w", rowsErr)
	}

	for _, r := range expired {
		id := r.id
		action := ActionConfirmationExpired
		if r.importID != nil && r.consentBasis == nil {
			action = ActionInviteExpired
		}
		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &id,
			Email:        r.email,
			Action:       action,
			ImportID:     r.importID,
		}); err != nil {
			return 0, fmt.Errorf("subscribers: recording %s for %d: %w", action, r.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("subscribers: committing expire-pending sweep tx: %w", err)
	}
	return int64(len(expired)), nil
}

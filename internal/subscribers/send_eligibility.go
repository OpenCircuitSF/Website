// send_eligibility.go is internal/mailing's send-time backstop for
// transactional mail (#0365): OutboxWorker.sendGate calls SendEligibility
// as the first statement of sendOne, immediately before render, re-checking
// the subscriber's LIVE state rather than trusting anything decided at
// enqueue time. It mirrors internal/mailing/audience.go's RecheckEligibleTx
// predicate-for-predicate and word-for-word (live status, suppressions,
// live-email drift) — see that file's package doc comment for why the
// check belongs immediately before the send, and #0365's plan §1 for why
// this is the transactional-mail twin of RecheckEligibleTx/MarkSkippedTx.
//
// Lives in this package, not internal/mailing, because internal/subscribers
// owns both tables this reads (subscribers, suppressions); audience.go
// crosses that boundary the other way only because a campaign recheck is a
// set-based anti-join over a whole batch of rows, which does not apply to
// evaluating a single outbound_queue row.
//
// #0340 added ConfirmToken (below): sendGate's status/suppression/email
// predicates re-check WHO the row is for, but nothing previously re-checked
// WHETHER the token a KindConfirmation/KindImportInvite row's payload
// carries is still the live one — a row deferred long enough by
// deferMissingPhysicalAddress can otherwise be delivered after the token it
// carries was rotated by an admin resend or cleared by
// (*Store).ExpirePendingSweep, or re-armed by (*Store).RestartSignup moving
// status back to 'pending' while the row still carries the pre-restart
// token. See #0340's plan §2/§3 for the full enumeration of who rotates
// what, and internal/mailing/outbox_worker.go's tokenGatedKinds for which
// kinds this applies to.
package subscribers

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SendEligibility is one subscriber's live state, as of the moment
// (*Store).SendEligibility ran — the facts OutboxWorker.sendGate needs to
// decide whether an already-claimed outbound_queue row should still be
// sent.
type SendEligibility struct {
	// Found is false when the subscriber id no longer has a subscribers
	// row (pgx.ErrNoRows). Unreachable in practice — outbound_queue's
	// subscriber_id column is ON DELETE CASCADE, so the row itself would
	// be gone too — but the caller must still decide what "no subscriber"
	// means: #0365's plan §6 rules it fail-closed (skip), since this is a
	// positively-learned fact, not a transient read error.
	Found bool
	// Status is the subscriber's live subscribers.status.
	Status string
	// Email is the subscriber's live subscribers.email — for the caller's
	// email-drift predicate against outbound_queue.recipient.
	Email string
	// Suppressed reports whether Email carries any suppressions row,
	// reason-blind (matching IsSuppressed's own doc comment) — a status
	// check cannot subsume this, since a suppression can exist against a
	// subscriber whose status is untouched.
	Suppressed bool
	// ConfirmToken is the subscriber's LIVE subscribers.confirm_token, as
	// of this read — nil when the column is NULL. nil is a POSITIVE fact
	// (#0340), not a read failure: it means the token has been consumed by
	// a real Confirm/Unsubscribe (in which case Status has already moved
	// off the value sendGate's gatedKinds wants and the status predicate
	// alone withholds the row), or swept by ExpirePendingSweep while
	// status stays 'pending' on purpose (#0128) — the case the status
	// predicate cannot see on its own. The caller compares this against
	// the payload's own confirm_token for KindConfirmation/KindImportInvite
	// rows only; every other kind ignores this field entirely.
	ConfirmToken *string
}

// SendEligibility reads subscriberID's live status, email, suppression
// state, and confirm_token in one round trip, mirroring RecheckEligibleTx's
// own SELECT (internal/mailing/audience.go) predicate-for-predicate, plus
// confirm_token for #0340. Returns SendEligibility{Found: false} with a nil
// error when no subscribers row exists for subscriberID — see that field's
// own doc comment for why that is not treated as an error.
func (s *Store) SendEligibility(ctx context.Context, subscriberID int64) (SendEligibility, error) {
	var e SendEligibility
	err := s.pool.QueryRow(ctx,
		`SELECT s.status, s.email, s.confirm_token,
		        EXISTS (SELECT 1 FROM suppressions x WHERE x.email = s.email)
		   FROM subscribers s
		  WHERE s.id = $1`,
		subscriberID,
	).Scan(&e.Status, &e.Email, &e.ConfirmToken, &e.Suppressed)
	if errors.Is(err, pgx.ErrNoRows) {
		return SendEligibility{Found: false}, nil
	}
	if err != nil {
		return SendEligibility{}, fmt.Errorf("subscribers: reading send eligibility for subscriber %d: %w", subscriberID, err)
	}
	e.Found = true
	return e, nil
}

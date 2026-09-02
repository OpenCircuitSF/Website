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
}

// SendEligibility reads subscriberID's live status, email, and suppression
// state in one round trip, mirroring RecheckEligibleTx's own SELECT
// (internal/mailing/audience.go) predicate-for-predicate. Returns
// SendEligibility{Found: false} with a nil error when no subscribers row
// exists for subscriberID — see that field's own doc comment for why that
// is not treated as an error.
func (s *Store) SendEligibility(ctx context.Context, subscriberID int64) (SendEligibility, error) {
	var e SendEligibility
	err := s.pool.QueryRow(ctx,
		`SELECT s.status, s.email,
		        EXISTS (SELECT 1 FROM suppressions x WHERE x.email = s.email)
		   FROM subscribers s
		  WHERE s.id = $1`,
		subscriberID,
	).Scan(&e.Status, &e.Email, &e.Suppressed)
	if errors.Is(err, pgx.ErrNoRows) {
		return SendEligibility{Found: false}, nil
	}
	if err != nil {
		return SendEligibility{}, fmt.Errorf("subscribers: reading send eligibility for subscriber %d: %w", subscriberID, err)
	}
	e.Found = true
	return e, nil
}

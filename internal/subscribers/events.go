// events.go adds subscriber_events (migrations/000022, PRD §6.11) as a
// second append-only log this package owns, alongside subscribers/
// subscriber_interests/suppressions.
//
// # Three logs, three jobs — why this is not audit_log
//
// The temptation is to route these ~20 machine-generated per-address events
// through internal/audit instead of standing up a second table. Four
// reasons this package has its own table instead, the last two dispositive
// (see #0126's plan §6 for the argument in full):
//
//  1. Different subject. An audit_log row is keyed to an actor and a
//     target; a subscriber_events row is keyed to an ADDRESS that must
//     outlive the subscribers row entirely. audit_log has no email column,
//     so the snapshot would have to go in Metadata.
//  2. That directly attacks internal/handlers'
//     audit_email_metadata_guard_test.go (#0237/#0252): it walks every
//     audit.Entry{} literal and pins the set of sites whose Metadata
//     carries an email against a fixture, because the privacy policy
//     enumerates exactly that set. Routing ~20 per-address actions through
//     audit_log with the email in Metadata would not defeat that guard —
//     it would SWAMP it, which is worse: the fixture would balloon and the
//     privacy page's promise would get materially harder to keep honest.
//  3. audit_log has no update path, by design (see that package's doc
//     comment: rows "are only ever inserted; they are never updated or
//     deleted"). The erasure criterion here REQUIRES redacting the email
//     column in place (see Erase, erase.go). Adding an UPDATE path to
//     audit_log to serve this one caller would break that invariant for
//     every row in the table, including the auth ceremonies'.
//  4. Volume and audience. #0114 filters the audit screen by
//     target_type/target_id for staff review of console actions. A
//     delivered event per address per campaign would bury it.
//
// The two logs coexist and neither replaces the other — do not delete an
// existing audit.Entry{} call site (auditSignup and friends in
// internal/handlers) on the theory that subscriber_events now covers it.
//
// # The closed action set cannot all be written by #0126 alone
//
// PRD §6.11's table lists 22 actions. Roughly nine have no point of
// occurrence anywhere in this tree yet — delivered needs #0124's SES
// delivery-event handler, confirmation_expired needs #0128's expiry sweep,
// welcome_sent needs #0127, imported/import_revoked/invite_* need #0125/
// #0129. This package defines the WHOLE closed set now (that is the
// contract those later issues code against) but only WRITES the actions
// whose call site already exists — see TestSubscriberEventActions_
// EveryConstantHasCallSiteOrOwner, which maps every constant to either a
// live call site or an explicit owner issue so the set cannot rot silently.
package subscribers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Action is one subscriber_events.action value — a closed set validated in
// Go (RecordEventTx returns ErrUnknownAction for anything outside it)
// rather than a database CHECK constraint, matching outbound_queue.kind's
// precedent: PRD §6.11's table is not phrased as a CHECK, and a Go-side
// validator gives callers a typed error instead of a raw
// constraint-violation to pattern-match.
type Action string

// The closed set, verbatim from PRD §6.11. See this file's package doc
// comment and TestSubscriberEventActions_EveryConstantHasCallSiteOrOwner
// for which of these #0126 actually writes today.
const (
	ActionSignupRequested     Action = "signup_requested"
	ActionConfirmationSent    Action = "confirmation_sent"
	ActionConfirmed           Action = "confirmed"
	ActionConfirmationExpired Action = "confirmation_expired" // owner: #0128
	ActionWelcomeSent         Action = "welcome_sent"         // owner: #0127
	ActionInterestsChanged    Action = "interests_changed"
	ActionUnsubscribed        Action = "unsubscribed"
	ActionResubscribed        Action = "resubscribed"
	ActionImported            Action = "imported"        // owner: #0125
	ActionInviteSent          Action = "invite_sent"     // owner: #0129
	ActionInviteAccepted      Action = "invite_accepted" // owner: #0129
	ActionInviteExpired       Action = "invite_expired"  // owner: #0129
	ActionImportRevoked       Action = "import_revoked"  // owner: #0125
	ActionCampaignSent        Action = "campaign_sent"
	ActionBouncedSoft         Action = "bounced_soft"
	ActionBouncedHard         Action = "bounced_hard"
	ActionComplained          Action = "complained"
	ActionDelivered           Action = "delivered" // owner: #0124
	ActionSuppressed          Action = "suppressed"
	ActionUnsuppressed        Action = "unsuppressed"
	ActionAdminEdited         Action = "admin_edited"
	ActionErased              Action = "erased"
)

// knownActions is the closed set RecordEventTx validates against.
var knownActions = map[Action]bool{
	ActionSignupRequested:     true,
	ActionConfirmationSent:    true,
	ActionConfirmed:           true,
	ActionConfirmationExpired: true,
	ActionWelcomeSent:         true,
	ActionInterestsChanged:    true,
	ActionUnsubscribed:        true,
	ActionResubscribed:        true,
	ActionImported:            true,
	ActionInviteSent:          true,
	ActionInviteAccepted:      true,
	ActionInviteExpired:       true,
	ActionImportRevoked:       true,
	ActionCampaignSent:        true,
	ActionBouncedSoft:         true,
	ActionBouncedHard:         true,
	ActionComplained:          true,
	ActionDelivered:           true,
	ActionSuppressed:          true,
	ActionUnsuppressed:        true,
	ActionAdminEdited:         true,
	ActionErased:              true,
}

// ErrUnknownAction is returned by RecordEventTx when Action is not one of
// the constants above — a programming error, per #0126's acceptance
// criteria, not a value that should ever reach the database.
var ErrUnknownAction = errors.New("subscribers: unknown subscriber_events action")

// ErrEventEmailRequired is returned when Email is empty. The snapshot is
// what makes a row survive erasure of the subscribers row; a row with no
// email defeats the whole reason this table exists.
var ErrEventEmailRequired = errors.New("subscribers: subscriber_events row requires a non-empty email snapshot")

// Event is one subscriber_events row to be written.
type Event struct {
	// SubscriberID is nil for a row about an address with no subscribers
	// row (there is none today, but the type allows it) or once erasure
	// has nulled it.
	SubscriberID *int64
	// Email is the snapshot — always required, always the value AT THE
	// TIME of the action, not re-read later.
	Email      string
	Action     Action
	CampaignID *int64
	// ActorUserID is nil when the subscriber or a webhook (SES) acted,
	// non-nil only for a staff-driven action (admin_edited).
	ActorUserID *int64
	// Detail is marshalled to JSONB. A nil Detail stores SQL NULL.
	Detail any
}

// RecordEventTx inserts e through q — the pool or a caller-owned
// transaction — validating Action against the closed set and Email as
// non-empty before ever reaching the database. Use this (not RecordEvent)
// from any caller whose event must commit atomically with the state change
// that caused it, matching internal/audit's WriteTx and internal/outbox's
// EnqueueTx idiom for the identical reason.
func RecordEventTx(ctx context.Context, q querier, e Event) error {
	if !knownActions[e.Action] {
		return fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
	}
	if e.Email == "" {
		return ErrEventEmailRequired
	}

	var detailJSON any
	if e.Detail != nil {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("subscribers: marshalling event detail for %q: %w", e.Action, err)
		}
		detailJSON = b
	}

	_, err := q.Exec(ctx,
		`INSERT INTO subscriber_events
		     (subscriber_id, email, action, campaign_id, actor_user_id, detail)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		e.SubscriberID, e.Email, string(e.Action), e.CampaignID, e.ActorUserID, detailJSON,
	)
	if err != nil {
		return fmt.Errorf("subscribers: recording event %q for %q: %w", e.Action, e.Email, err)
	}
	return nil
}

// RecordEvent is RecordEventTx against the shared pool.
func (s *Store) RecordEvent(ctx context.Context, e Event) error {
	return RecordEventTx(ctx, s.pool, e)
}

// SubscriberEventRow is one row read back for the subscriber detail drawer
// (#0032).
type SubscriberEventRow struct {
	Action     Action
	CreatedAt  time.Time
	CampaignID *int64
	Detail     []byte // raw JSONB; nil when the row carried no detail
}

// subscriberEventHistoryLimit bounds the subscriber drawer's event list
// (#0126's plan §7: "bounded (100 rows, no pagination)").
const subscriberEventHistoryLimit = 100

// EventHistory returns subscriberID's subscriber_events rows, newest
// first, bounded to subscriberEventHistoryLimit — the subscriber detail
// drawer's read path, via idx_subscriber_events_subscriber.
func (s *Store) EventHistory(ctx context.Context, subscriberID int64) ([]SubscriberEventRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT action, created_at, campaign_id, detail
		   FROM subscriber_events
		  WHERE subscriber_id = $1
		  ORDER BY created_at DESC, id DESC
		  LIMIT $2`,
		subscriberID, subscriberEventHistoryLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("subscribers: loading event history for %d: %w", subscriberID, err)
	}
	defer rows.Close()

	var out []SubscriberEventRow
	for rows.Next() {
		var r SubscriberEventRow
		var action string
		if err := rows.Scan(&action, &r.CreatedAt, &r.CampaignID, &r.Detail); err != nil {
			return nil, fmt.Errorf("subscribers: scanning event history row: %w", err)
		}
		r.Action = Action(action)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating event history rows: %w", err)
	}
	return out, nil
}

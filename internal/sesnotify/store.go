// store.go adds Store, the data-access layer over email_events (#0038,
// migrations/000014), to this package.
//
// SQL for this table lives here, not in internal/handlers (CLAUDE.md §1):
// the handler that ingests SES notifications depends on this type, never on
// raw SQL of its own.
//
// InsertTx takes a querier (the Exec/QueryRow/Query subset of pgx,
// satisfied by both *pgxpool.Pool and pgx.Tx) rather than the pool
// directly, mirroring internal/audit's Write/WriteTx split and
// internal/subscribers' SuppressionStore.Add/AddTx: #0038's handler writes
// one email_events row PER RECIPIENT, one or two suppressions rows, and a
// subscriber status change, all for a single incoming SNS message, and all
// of it must land in one transaction or not at all (see #0033's hard
// block, recorded in issues/0033.md and issues/0038.md §5) — a crash
// between the event write and the suppression write would leave the retry
// deduped away by this table's own UNIQUE constraint, with the suppression
// permanently missing.
package sesnotify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the subset of pgx Store uses. *pgxpool.Pool and pgx.Tx both
// satisfy it, so InsertTx runs identically against the pool or inside a
// caller-owned transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// EmailEvent is a single row of the email_events table.
type EmailEvent struct {
	ID            int64
	SNSMessageID  string
	SESMessageID  *string
	EventType     string
	BounceType    *string
	BounceSubtype *string
	Recipient     string
	EventAt       *time.Time
	Payload       []byte
	ReceivedAt    time.Time
}

// NewEmailEvent is the input to InsertTx: one row, scoped to one recipient,
// for one incoming SNS message. Empty strings map to SQL NULL for the
// nullable columns (SESMessageID, BounceType, BounceSubtype), matching this
// codebase's nullIfEmpty convention elsewhere (internal/subscribers,
// internal/audit). Recipient is normalised lower(trim(...)) in SQL, not
// here — see event.go's package doc comment for why.
type NewEmailEvent struct {
	SNSMessageID  string
	SESMessageID  string
	EventType     string
	BounceType    string
	BounceSubtype string
	Recipient     string
	EventAt       *time.Time
	Payload       []byte // valid JSON; ses_notifications.go guarantees this even for an unparseable inner payload
}

// Store is the data-access layer over email_events.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool. The pool is
// used only by ByMessageID (a plain read, for tests and any future admin
// view); every write goes through InsertTx against a caller-supplied
// querier — see the package doc comment.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const emailEventColumns = `id, sns_message_id, ses_message_id, event_type,
	bounce_type, bounce_subtype, recipient, event_at, payload, received_at`

func scanEmailEvent(row pgx.Row) (EmailEvent, error) {
	var ev EmailEvent
	if err := row.Scan(
		&ev.ID, &ev.SNSMessageID, &ev.SESMessageID, &ev.EventType,
		&ev.BounceType, &ev.BounceSubtype, &ev.Recipient, &ev.EventAt, &ev.Payload, &ev.ReceivedAt,
	); err != nil {
		return EmailEvent{}, err
	}
	return ev, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// InsertTx inserts one email_events row for in.Recipient inside q, and
// reports whether a row was actually added. #0038's dedupe key is
// (sns_message_id, recipient) (migrations/000014) — SNS's at-least-once
// delivery means the identical message can arrive again, and "ON CONFLICT
// DO NOTHING" plus inserted=false is how the caller recognizes that
// redelivery and short-circuits the rest of its transaction (see
// internal/handlers/ses_notifications.go) rather than reprocessing state
// changes that already landed.
//
// The insert happens BEFORE any interpretation of the event — PRD §6.7
// item 3 — so in.Payload should be the raw inner SES event JSON regardless
// of whether the caller could make sense of it.
func (s *Store) InsertTx(ctx context.Context, q querier, in NewEmailEvent) (id int64, inserted bool, err error) {
	row := q.QueryRow(ctx,
		`INSERT INTO email_events
		     (sns_message_id, ses_message_id, event_type, bounce_type, bounce_subtype, recipient, event_at, payload)
		 VALUES ($1, $2, $3, $4, $5, lower(trim($6)), $7, $8)
		 ON CONFLICT (sns_message_id, recipient) DO NOTHING
		 RETURNING id`,
		in.SNSMessageID, nullIfEmpty(in.SESMessageID), in.EventType,
		nullIfEmpty(in.BounceType), nullIfEmpty(in.BounceSubtype),
		in.Recipient, in.EventAt, in.Payload,
	)
	err = row.Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The ON CONFLICT arm fired: this (sns_message_id, recipient) pair
		// was already recorded by an earlier, successfully committed
		// delivery of this exact SNS message. Not an error.
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("sesnotify: inserting email event for message %q recipient %q: %w",
			in.SNSMessageID, in.Recipient, err)
	}
	return id, true, nil
}

// ByRecipient returns every email_events row for one normalized recipient
// address, newest first — #0124's GET /admin/deliverability/{email}: the
// full per-address bounce/complaint/delivery history an admin reads. Served
// by idx_email_events_recipient_time (migrations/000014, edited directly by
// #0124 — see that migration's comment).
//
// This replaces #0039/#0109's CountRecentSoftBounces(Pool), which counted
// rows within a rolling window to decide whether to suppress. #0124 retires
// that rule (the suppression decision is now a consecutive streak on
// subscribers.soft_bounce_streak, counted incrementally rather than
// re-queried from this table — see ses_notifications.go's applyBounce) —
// this table's only remaining job is the immutable history behind that
// decision, which is exactly what ByRecipient reads.
func (s *Store) ByRecipient(ctx context.Context, recipient string) ([]EmailEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+emailEventColumns+` FROM email_events
		  WHERE recipient = lower(trim($1))
		  ORDER BY received_at DESC, id DESC`,
		recipient,
	)
	if err != nil {
		return nil, fmt.Errorf("sesnotify: listing email events for recipient %q: %w", recipient, err)
	}
	defer rows.Close()

	var out []EmailEvent
	for rows.Next() {
		ev, err := scanEmailEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("sesnotify: scanning email event row: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sesnotify: listing email events for recipient %q: %w", recipient, err)
	}
	return out, nil
}

// ByMessageID returns every row recorded for one SNS MessageId, newest
// first. Used by tests to assert "exactly one row per recipient" (the
// redelivery and multi-recipient properties) without hand-writing SQL in
// the test file; no production caller exists yet.
func (s *Store) ByMessageID(ctx context.Context, snsMessageID string) ([]EmailEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+emailEventColumns+` FROM email_events WHERE sns_message_id = $1 ORDER BY id DESC`,
		snsMessageID,
	)
	if err != nil {
		return nil, fmt.Errorf("sesnotify: listing email events for message %q: %w", snsMessageID, err)
	}
	defer rows.Close()

	var out []EmailEvent
	for rows.Next() {
		ev, err := scanEmailEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("sesnotify: scanning email event row: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sesnotify: listing email events for message %q: %w", snsMessageID, err)
	}
	return out, nil
}

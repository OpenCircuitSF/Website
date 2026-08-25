// suppressions.go adds the global send-block list (PRD §6.2, §6.5; #0033) as
// a second table this package owns, alongside subscribers/subscriber_interests.
//
// SuppressionStore is deliberately its own type — not more methods on
// Store — because, unlike every subscribers-table mutation, a suppression
// write must sometimes commit atomically with a caller's own transaction.
// #0038 (SES bounce/complaint ingestion, blocked on this issue per its
// planning pass — see issues/0033.md's "Criterion added 2026-08-19") writes
// an email_events row and a suppressions row for the same incoming
// notification; if those were two independent pool writes, a crash between
// them would leave a hard-bounced address deduped away on retry with no
// suppression ever recorded — a silent hole in exactly the mechanism this
// table exists to provide. Add/AddTx below follow the querier/WriteTx idiom
// internal/audit's Logger.Write/WriteTx already establishes for the
// identical reason (see that package's doc comment).
//
// Suppression is keyed by NORMALIZED EMAIL, not subscriber id — there is
// deliberately no foreign key to subscribers. That is what lets a
// suppression survive a subscriber's hard deletion (#0060) and block a
// future resignup by the same address even when no subscribers row exists
// for it, either at the moment the suppression was added or at the moment
// it is later checked.
package subscribers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Suppression reasons, matching the suppressions_reason_check CHECK
// constraint (migrations/000012).
const (
	SuppressionReasonHardBounce         = "hard_bounce"
	SuppressionReasonComplaint          = "complaint"
	SuppressionReasonManual             = "manual"
	SuppressionReasonRepeatedSoftBounce = "repeated_soft_bounce"
)

// suppressionReasons is the closed set of legal values. Validating it in Go
// (rather than relying solely on the database's own CHECK) means a caller
// gets a typed ErrInvalidSuppressionReason instead of a raw pgconn error to
// pattern-match against.
var suppressionReasons = map[string]bool{
	SuppressionReasonHardBounce:         true,
	SuppressionReasonComplaint:          true,
	SuppressionReasonManual:             true,
	SuppressionReasonRepeatedSoftBounce: true,
}

// ErrInvalidSuppressionReason is returned by Add/AddTx when Reason is not one
// of the four SuppressionReason* constants above.
var ErrInvalidSuppressionReason = errors.New("subscribers: invalid suppression reason")

// ErrSuppressionNotFound is returned by Remove when no suppressions row
// matches the given email.
var ErrSuppressionNotFound = errors.New("subscribers: suppression not found")

// Suppression is a single row of the suppressions table.
type Suppression struct {
	Email     string
	Reason    string
	Note      *string
	CreatedAt time.Time
}

// NewSuppression is the input to Add/AddTx.
type NewSuppression struct {
	Email  string
	Reason string
	Note   string // empty maps to SQL NULL, matching NewSignup's convention elsewhere in this package
}

// querier is the subset of pgx SuppressionStore uses. *pgxpool.Pool and
// pgx.Tx both satisfy it, so the same statements run against the pool
// directly (Add) or inside a caller-owned transaction (AddTx) — the same
// split internal/audit's querier/Write/WriteTx makes, for the same reason.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// SuppressionStore is the data-access layer over the suppressions table.
type SuppressionStore struct {
	pool *pgxpool.Pool
}

// NewSuppressionStore constructs a SuppressionStore over the shared
// connection pool.
func NewSuppressionStore(pool *pgxpool.Pool) *SuppressionStore {
	return &SuppressionStore{pool: pool}
}

const suppressionColumns = `email, reason, note, created_at`

// scanSuppression accepts pgx.Row's single-method interface, which pgx.Rows
// also satisfies — the same helper works for QueryRow (Add/IsSuppressed) and
// for each row.Next() iteration in List.
func scanSuppression(row pgx.Row) (Suppression, error) {
	var sup Suppression
	if err := row.Scan(&sup.Email, &sup.Reason, &sup.Note, &sup.CreatedAt); err != nil {
		return Suppression{}, err
	}
	return sup, nil
}

// Add idempotently adds an (email, reason) pair to the suppression list
// through the shared pool. "Idempotent" here means: a repeat Add for a
// reason already recorded for this address returns the EXISTING row
// unchanged (its original note and created_at) rather than erroring or
// overwriting it. A DIFFERENT reason for the same address is not a repeat —
// it inserts a second, independent row (#0100): an address that both
// hard-bounced and complained must be representable as two rows, or clearing
// one reason later would have no way to avoid destroying the other. Use
// AddTx instead when the caller already owns an open transaction the write
// must commit or roll back atomically with — see the package doc comment.
func (s *SuppressionStore) Add(ctx context.Context, in NewSuppression, now time.Time) (Suppression, error) {
	return addSuppression(ctx, s.pool, in, now)
}

// AddTx is the transaction-scoped twin of Add: it runs the identical
// upsert against q instead of the pool, so a caller that owns an open
// pgx.Tx (#0038 ingesting an SES event alongside its own email_events
// insert is the motivating case — see the package doc comment) can commit or
// roll back the suppression row atomically with the rest of its work.
func (s *SuppressionStore) AddTx(ctx context.Context, q querier, in NewSuppression, now time.Time) (Suppression, error) {
	return addSuppression(ctx, q, in, now)
}

// addSuppression is the shared implementation behind Add/AddTx. The
// "ON CONFLICT (email, reason) DO UPDATE SET email = suppressions.email" is a
// deliberate no-op update: a plain "DO NOTHING" would make RETURNING produce
// no row at all on a conflict, and this call's whole idempotency contract is
// that it always returns the (possibly pre-existing) row. Assigning the
// column to itself changes no data but keeps RETURNING active, so a repeat
// Add for the SAME (email, reason) pair returns the ORIGINAL note/created_at,
// never the newly-submitted ones. The conflict target is (email, reason), not
// email alone (#0100, migrations/000013) — a different reason for the same
// address is a distinct row, not a conflict.
func addSuppression(ctx context.Context, q querier, in NewSuppression, now time.Time) (Suppression, error) {
	if !suppressionReasons[in.Reason] {
		return Suppression{}, ErrInvalidSuppressionReason
	}
	row := q.QueryRow(ctx,
		`INSERT INTO suppressions (email, reason, note, created_at)
		 VALUES (lower(trim($1)), $2, $3, $4)
		 ON CONFLICT (email, reason) DO UPDATE SET email = suppressions.email
		 RETURNING `+suppressionColumns,
		in.Email, in.Reason, nullIfEmpty(in.Note), now,
	)
	sup, err := scanSuppression(row)
	if err != nil {
		return Suppression{}, fmt.Errorf("subscribers: adding suppression for %q: %w", in.Email, err)
	}

	// #0126: suppressed. SubscriberID is nil here — suppressions is keyed
	// by email, not subscriber id, by design (see the package doc
	// comment), so this layer has no id to attach without an extra lookup
	// this call doesn't otherwise need. Note this INSERT ... ON CONFLICT
	// DO UPDATE is idempotent by design (a repeat Add for the same
	// (email, reason) returns the original row unchanged) — a repeat call
	// therefore writes a repeat "suppressed" event too, since this layer
	// cannot cheaply distinguish "inserted" from "already existed" without
	// an extra round trip this call doesn't otherwise need. Accepted: an
	// extra history row is harmless (an operator reading the address's
	// timeline sees "still suppressed," not a false new fact), where
	// missing a genuine one would not be.
	if err := RecordEventTx(ctx, q, Event{
		Email:  sup.Email,
		Action: ActionSuppressed,
		Detail: map[string]any{"reason": sup.Reason},
	}); err != nil {
		return Suppression{}, fmt.Errorf("subscribers: recording suppressed event for %q: %w", in.Email, err)
	}
	return sup, nil
}

// IsSuppressed reports whether email — after the same lower(trim(...))
// normalization every other write in this package uses — has a
// suppressions row. Its signature satisfies handlers.SuppressionChecker, so
// a *SuppressionStore can be wired directly into handlers.NewSubscribeHandler
// as the real implementation of that seam — see cmd/opencircuit/main.go.
//
// Carried in from #0026's review (issues/0033.md): this issues exactly one
// round trip, matching FindByEmail's own single round trip. Subscribe
// (#0088) calls both unconditionally, in the same fixed order, for every
// accepted request regardless of branch; a zero-round-trip short-circuit
// here (the way the placeholder NoSuppressions did, and the way this method
// must NOT) would reopen the timing channel #0088 closed by making the
// suppressed branch cheaper than its peers again.
//
// Deliberately reason-blind (#0100): any suppressions row of any reason
// blocks. The send gate's question — "may we mail this address" — does not
// depend on WHY the address was suppressed, so this stays a single
// EXISTS(...) with no reason predicate even after the primary key widened to
// (email, reason).
func (s *SuppressionStore) IsSuppressed(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM suppressions WHERE email = lower(trim($1)))`,
		email,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("subscribers: checking suppression for %q: %w", email, err)
	}
	return exists, nil
}

// SuppressionListItem is one row of SuppressionStore.List's admin-facing
// view: the four suppressions columns plus the subscriber_status a LEFT JOIN
// against subscribers resolves for the same email, or nil when no
// subscribers row exists for that address (an orphan — hard deletion,
// #0060, or a suppression added before any signup). #0100's admin
// suppressions screen needs this to answer "is this address blocked at the
// suppression list, the subscriber row, or both?" without an N+1 query per
// row, and to let the client pre-disable a `complaint` removal on a row that
// still has a live subscriber (see admin_suppressions.go).
type SuppressionListItem struct {
	Suppression
	SubscriberStatus *string
}

// List returns every suppression row joined to its subscriber's status (nil
// when orphaned), ordered by email then newest-first within an email
// (#0100). The per-email grouping is deliberate: since the primary key
// widened to (email, reason), one address can appear more than once, and
// ordering by email keeps that address's rows adjacent so an admin can see
// every reason it is blocked for at a glance — the whole point of this
// issue. No pagination or filtering — #0033's acceptance criteria ask only
// for "list"; a future screen can add those against this same query if the
// table grows large enough to need them.
func (s *SuppressionStore) List(ctx context.Context) ([]SuppressionListItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.email, s.reason, s.note, s.created_at, sub.status
		   FROM suppressions s
		   LEFT JOIN subscribers sub ON sub.email = s.email
		  ORDER BY s.email ASC, s.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("subscribers: listing suppressions: %w", err)
	}
	defer rows.Close()

	var out []SuppressionListItem
	for rows.Next() {
		var item SuppressionListItem
		if err := rows.Scan(&item.Email, &item.Reason, &item.Note, &item.CreatedAt, &item.SubscriberStatus); err != nil {
			return nil, fmt.Errorf("subscribers: scanning suppression row: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: listing suppressions: %w", err)
	}
	return out, nil
}

// ListByEmail returns every suppression row for one normalized address,
// newest first. #0100: an address can now carry more than one row (one per
// reason), so callers that need to know "what else is this address
// suppressed for" — ClearComplaint reporting survivors, and the admin
// removal handler — call this instead of a single-row lookup.
func (s *SuppressionStore) ListByEmail(ctx context.Context, email string) ([]Suppression, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+suppressionColumns+` FROM suppressions WHERE email = lower(trim($1)) ORDER BY created_at DESC`,
		email,
	)
	if err != nil {
		return nil, fmt.Errorf("subscribers: listing suppressions for %q: %w", email, err)
	}
	defer rows.Close()

	var out []Suppression
	for rows.Next() {
		sup, err := scanSuppression(rows)
		if err != nil {
			return nil, fmt.Errorf("subscribers: scanning suppression row: %w", err)
		}
		out = append(out, sup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: listing suppressions for %q: %w", email, err)
	}
	return out, nil
}

// Remove deletes the suppressions row matching BOTH email and reason
// (#0100) — there is deliberately no reason-less removal, because a
// reason-blind DELETE is exactly the defect this issue exists to close: an
// address suppressed for both hard_bounce and complaint must be able to have
// one reason cleared while the other stays in force. reason must be one of
// the four SuppressionReason* constants (ErrInvalidSuppressionReason
// otherwise). Removing a suppression must always be an explicit, audited
// admin action (PRD §6.2, CLAUDE.md §9's spirit for `complained` applies
// equally here) — this method itself does not check "is the caller an
// admin" or write an audit row; that is the caller's responsibility (see
// internal/handlers/admin_subscribers.go's ClearComplaint and
// admin_suppressions.go's removal handler, the two production call sites).
// Returns the deleted row (not just an error) because the row is about to be
// destroyed and the caller's audit entry is the only remaining record of its
// note/created_at. Returns ErrSuppressionNotFound if no row matched email
// AND reason together, so a caller can distinguish "there was nothing to
// remove" from a genuine failure.
func (s *SuppressionStore) Remove(ctx context.Context, email, reason string) (Suppression, error) {
	if !suppressionReasons[reason] {
		return Suppression{}, ErrInvalidSuppressionReason
	}
	row := s.pool.QueryRow(ctx,
		`DELETE FROM suppressions WHERE email = lower(trim($1)) AND reason = $2 RETURNING `+suppressionColumns,
		email, reason,
	)
	sup, err := scanSuppression(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Suppression{}, ErrSuppressionNotFound
	case err != nil:
		return Suppression{}, fmt.Errorf("subscribers: removing suppression for %q reason %q: %w", email, reason, err)
	}

	// #0126: unsuppressed. The DELETE above already committed (this is a
	// pool call, not a transaction), so a failure here does not undo the
	// removal — it only means the removal happened without this row of
	// evidence, which the caller (admin_suppressions.go) logs rather than
	// treating as if the removal itself failed.
	if err := RecordEventTx(ctx, s.pool, Event{
		Email:  sup.Email,
		Action: ActionUnsuppressed,
		Detail: map[string]any{"reason": sup.Reason},
	}); err != nil {
		return sup, fmt.Errorf("subscribers: recording unsuppressed event for %q: %w", email, err)
	}
	return sup, nil
}

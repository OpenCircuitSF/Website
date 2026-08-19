// Package subscribers is the data-access layer for the mailing list (PRD
// §6.2, §6.3): the subscribers table (list membership independent of any
// user account, plus the consent evidence that makes the list defensible)
// and the subscriber_interests join table.
//
// Email normalization: every write path lowercases and trims the address
// before it reaches SQL (normalizeEmail), and the database backstops that
// with the subscribers_email_normalized CHECK constraint added in
// migrations/000010_create_subscribers.up.sql. Per the issue's notes, Gmail
// dots and "+tag" suffixes are never stripped — they are distinct addresses
// per RFC 5321/5322 and people use them deliberately to segment their own
// mail.
//
// Tokens: confirm_token and manage_token are 32 random bytes from
// crypto/rand, base64url-encoded (tokens.go) — not an HMAC of the email
// (PRD §6.4). Random tokens are individually revocable by rotating the
// column and leak nothing if a URL ends up in a referrer header or a
// screenshot.
//
// complained never auto-resubscribes (CLAUDE.md §9): every status mutator in
// this package — Confirm, Unsubscribe, MarkBounced, MarkComplained (via the
// shared setStatus), and RestartSignup — refuses to move a subscriber out of
// the complained status. The check lives in one place
// (statusLockedFromNonAdmin, used by setStatus and by Unsubscribe's and
// RestartSignup's own UPDATEs) so a future mutator can't add a new unguarded
// status write the way an earlier version of this file did.
// AdminClearComplaint is the sole exception: it is the admin-only path
// (#0032) allowed to move a subscriber out of complained, and it is the only
// method that does not consult the guard.
package subscribers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status values, matching the subscribers_status_check CHECK constraint.
const (
	StatusPending      = "pending"
	StatusActive       = "active"
	StatusUnsubscribed = "unsubscribed"
	StatusBounced      = "bounced"
	StatusComplained   = "complained"
)

// Unsubscribe source values, matching the
// subscribers_unsubscribe_source_check CHECK constraint.
const (
	SourceOneClick    = "one_click"
	SourcePreferences = "preferences"
	SourceMailto      = "mailto"
	SourceAdmin       = "admin"
)

// ErrNotFound is returned when no subscribers row matches a lookup.
var ErrNotFound = errors.New("subscribers: not found")

// ErrEmailExists is returned by Create when a subscribers row already exists
// for the (normalized) email address.
var ErrEmailExists = errors.New("subscribers: email already exists")

// ErrTokenInvalid is returned when a confirm token is unknown or expired.
// Both cases map to the same error, as in internal/auth, so the confirmation
// handler can't be used as an oracle for which case occurred.
var ErrTokenInvalid = errors.New("subscribers: confirm token invalid or expired")

// ErrComplainedLocked is returned by Confirm when the token's subscriber is
// already in the complained status. Only an admin can clear that state
// (CLAUDE.md §9); a confirmation click can never do it.
var ErrComplainedLocked = errors.New("subscribers: subscriber has complained and cannot be reactivated by confirmation")

// ErrNotComplained is returned by AdminClearComplaint when the target
// subscriber is not currently in the complained status — there is nothing
// to clear. Distinguished from ErrNotFound so the admin screen (#0032) can
// tell "no such subscriber" apart from "that subscriber never complained".
var ErrNotComplained = errors.New("subscribers: subscriber is not complained; nothing to clear")

// statusLockedFromNonAdmin is the status that no non-admin mutator in this
// package may move a subscriber out of (CLAUDE.md §9). Every status write
// except AdminClearComplaint must consult this constant rather than writing
// status unconditionally — see the package doc comment.
const statusLockedFromNonAdmin = StatusComplained

// Subscriber is a single row of the subscribers table.
type Subscriber struct {
	ID                int64
	Email             string
	Status            string
	ConfirmToken      *string
	ConfirmSentAt     *time.Time
	ConfirmExpiresAt  *time.Time
	ConfirmedAt       *time.Time
	ManageToken       string
	SignupIP          *string
	SignupUserAgent   *string
	UTMSource         *string
	UTMMedium         *string
	UTMCampaign       *string
	UnsubscribedAt    *time.Time
	UnsubscribeSource *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewSignup is the input to Create: everything captured at the moment a
// visitor submits the signup form, including the consent evidence PRD §6.2
// requires (SignupIP, SignupUserAgent, and the CreatedAt/ConfirmSentAt
// timestamps Create derives from now).
type NewSignup struct {
	Email           string
	SignupIP        string // empty maps to SQL NULL; never fabricate a value
	SignupUserAgent string
	UTMSource       string
	UTMMedium       string
	UTMCampaign     string
	ConfirmTTL      time.Duration
}

// Store is the data-access layer over the subscribers and
// subscriber_interests tables.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// normalizeEmail lowercases and trims an address the same way every write
// path and the database CHECK constraint expect. It deliberately does NOT
// strip Gmail dots or "+tag" suffixes — see the package doc comment.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

const subscriberColumns = `id, email, status, confirm_token, confirm_sent_at,
	confirm_expires_at, confirmed_at, manage_token, host(signup_ip),
	signup_user_agent, utm_source, utm_medium, utm_campaign,
	unsubscribed_at, unsubscribe_source, created_at, updated_at`

func scanSubscriber(row pgx.Row) (Subscriber, error) {
	var sub Subscriber
	err := row.Scan(
		&sub.ID, &sub.Email, &sub.Status, &sub.ConfirmToken, &sub.ConfirmSentAt,
		&sub.ConfirmExpiresAt, &sub.ConfirmedAt, &sub.ManageToken, &sub.SignupIP,
		&sub.SignupUserAgent, &sub.UTMSource, &sub.UTMMedium, &sub.UTMCampaign,
		&sub.UnsubscribedAt, &sub.UnsubscribeSource, &sub.CreatedAt, &sub.UpdatedAt,
	)
	if err != nil {
		return Subscriber{}, err
	}
	return sub, nil
}

// nullIfEmpty maps an empty string to SQL NULL rather than an empty-string
// value, matching internal/audit's handling of the same INET-column pitfall:
// an empty string is not a valid INET literal.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Create inserts a new pending subscriber with freshly generated confirm and
// manage tokens, normalizing the email first. Returns ErrEmailExists if a row
// already exists for that (normalized) address — the caller (the #0026
// subscribe handler) is responsible for mapping that to the same uniform 202
// response as a brand-new signup, so the endpoint never becomes an
// email-enumeration oracle (CLAUDE.md §9).
//
// confirm_sent_at is deliberately left NULL here rather than stamped with
// now: #0026's review carried in the observation that stamping it at INSERT
// time — before the caller has even attempted the SES send — lets a send
// failure silently consume the once-per-hour resend window, leaving the
// person unable to retry for an hour despite never receiving a first email.
// The #0026 handler calls MarkConfirmationSent only after mailer.Send
// actually succeeds, so the resend cooldown is anchored to a real delivery
// attempt, not a delivery attempt that may never have left the process.
func (s *Store) Create(ctx context.Context, in NewSignup, now time.Time) (Subscriber, error) {
	email := normalizeEmail(in.Email)

	confirmToken, err := newToken()
	if err != nil {
		return Subscriber{}, err
	}
	manageToken, err := newToken()
	if err != nil {
		return Subscriber{}, err
	}
	confirmExpiresAt := now.Add(in.ConfirmTTL)

	row := s.pool.QueryRow(ctx,
		`INSERT INTO subscribers
		     (email, status, confirm_token, confirm_sent_at, confirm_expires_at,
		      manage_token, signup_ip, signup_user_agent,
		      utm_source, utm_medium, utm_campaign, created_at, updated_at)
		 VALUES
		     ($1, $2, $3, NULL, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		 RETURNING `+subscriberColumns,
		email, StatusPending, confirmToken, confirmExpiresAt,
		manageToken, nullIfEmpty(in.SignupIP), nullIfEmpty(in.SignupUserAgent),
		nullIfEmpty(in.UTMSource), nullIfEmpty(in.UTMMedium), nullIfEmpty(in.UTMCampaign), now,
	)
	sub, err := scanSubscriber(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Subscriber{}, ErrEmailExists
		}
		return Subscriber{}, fmt.Errorf("subscribers: creating %q: %w", email, err)
	}
	return sub, nil
}

// MarkConfirmationSent stamps confirm_sent_at = now. It is the compensating
// action for the confirm_sent_at decision documented on Create: nothing
// stamps this column at signup time, so the #0026 handler calls this method
// exactly once, immediately after a confirmation-email send actually
// succeeds — for a brand-new signup (Create), a restarted signup
// (RestartSignup), or a resend to an already-pending subscriber. If the send
// fails, the handler never calls this method, so confirm_sent_at (and
// therefore the once-per-hour resend cooldown the handler computes from it)
// stays exactly where it was and the caller can retry immediately.
//
// Deliberately does NOT consult statusLockedFromNonAdmin: this method never
// writes status, so there is nothing for the guard to protect against — a
// complained subscriber's confirm_sent_at is not part of the vocabulary
// CLAUDE.md §9 locks.
func (s *Store) MarkConfirmationSent(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET confirm_sent_at = $2, updated_at = $2
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, now,
	)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: marking confirmation sent for %d: %w", id, err)
	}
	return sub, nil
}

// FindByEmail looks up a subscriber by (normalized) email.
func (s *Store) FindByEmail(ctx context.Context, email string) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE email = $1`,
		normalizeEmail(email))
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: finding by email: %w", err)
	}
	return sub, nil
}

// FindByConfirmToken looks up a subscriber by its live confirm_token. Unlike
// Confirm, this does not check expiry or consume the token — it's a
// read-only lookup for e.g. re-rendering the confirmation page.
func (s *Store) FindByConfirmToken(ctx context.Context, token string) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE confirm_token = $1`, token)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: finding by confirm token: %w", err)
	}
	return sub, nil
}

// FindByManageToken looks up a subscriber by its manage_token (long-lived;
// no expiry). Backs the preference center (#0031) and one-click unsubscribe
// (#0034).
func (s *Store) FindByManageToken(ctx context.Context, token string) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE manage_token = $1`, token)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: finding by manage token: %w", err)
	}
	return sub, nil
}

// Confirm consumes a confirm_token: it validates the token exists and has
// not expired, refuses to reactivate a complained subscriber
// (ErrComplainedLocked), and otherwise transitions pending → active,
// stamping confirmed_at and clearing the token (single-use — a cleared token
// can never be replayed). Runs inside a transaction with SELECT ... FOR
// UPDATE so a concurrent double-click on the same link can't both succeed.
func (s *Store) Confirm(ctx context.Context, token string, now time.Time) (Subscriber, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: beginning confirm tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE confirm_token = $1 FOR UPDATE`, token)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrTokenInvalid
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: locking subscriber for confirm: %w", err)
	}

	if sub.ConfirmExpiresAt == nil || !sub.ConfirmExpiresAt.After(now) {
		return Subscriber{}, ErrTokenInvalid
	}
	if sub.Status == StatusComplained {
		return Subscriber{}, ErrComplainedLocked
	}

	row = tx.QueryRow(ctx,
		`UPDATE subscribers
		    SET status = $2, confirmed_at = $3, confirm_token = NULL,
		        confirm_expires_at = NULL, updated_at = $3
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		sub.ID, StatusActive, now,
	)
	updated, err := scanSubscriber(row)
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: activating subscriber %d: %w", sub.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: committing confirm tx: %w", err)
	}
	return updated, nil
}

// Unsubscribe transitions a subscriber to unsubscribed, stamping
// unsubscribed_at and recording source (one of the Source* constants). Safe
// to call on an already-unsubscribed or bounced row (idempotent, no error).
//
// If the subscriber is currently complained, this is a silent no-op: the
// row is returned unchanged (still complained) and no error is returned.
// That is deliberate, not an oversight. PRD §6.5 requires the one-click
// unsubscribe endpoint (#0034) to answer a neutral 200 for every token
// state, including one belonging to a complained subscriber — so this
// method cannot surface an error here the way Confirm surfaces
// ErrComplainedLocked; the caller (an unauthenticated POST from a mail
// provider, per RFC 8058) has no way to act on an error and PRD §6.5
// forbids failing the request. An earlier version of this method wrote
// status unconditionally, which overwrote a recorded complaint with
// "unsubscribed" and let a subsequent Confirm reach active — see this
// issue's Review notes for the reproduced chain. AdminClearComplaint is the
// only path that may move a subscriber out of complained.
func (s *Store) Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET status             = CASE WHEN status = $5 THEN status             ELSE $2 END,
		        unsubscribed_at    = CASE WHEN status = $5 THEN unsubscribed_at    ELSE $3 END,
		        unsubscribe_source = CASE WHEN status = $5 THEN unsubscribe_source ELSE $4 END,
		        updated_at         = CASE WHEN status = $5 THEN updated_at         ELSE $3 END
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, StatusUnsubscribed, now, source, statusLockedFromNonAdmin,
	)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: unsubscribing %d: %w", id, err)
	}
	return sub, nil
}

// RestartSignupInput is the input to RestartSignup: the consent evidence
// captured at the moment someone whose prior signup ended in unsubscribed (or
// bounced — see the #0026 handler's Gotchas for why that status is routed
// here too) submits the signup form again. Mirrors NewSignup's non-email
// fields; there is no Email field because RestartSignup operates on an
// existing row by id, not by address.
type RestartSignupInput struct {
	SignupIP        string // empty maps to SQL NULL; never fabricate a value
	SignupUserAgent string
	UTMSource       string
	UTMMedium       string
	UTMCampaign     string
	ConfirmTTL      time.Duration
}

// RestartSignup is the "unsubscribed → treat as a new signup; fresh confirm
// token" branch PRD §6.3 requires (the #0026 subscribe handler's Create
// returns ErrEmailExists for this case, and Create cannot itself transition
// an existing row). It generates a fresh confirm token, resets
// confirm_expires_at to now+ttl, clears confirmed_at and confirm_sent_at
// (mirroring Create — see MarkConfirmationSent), refreshes the consent
// evidence (signup_ip/user_agent/utm_*) to this new signup event, and moves
// status to pending.
//
// It guards statusLockedFromNonAdmin exactly like every other status mutator
// in this package (see the package doc comment): if the subscriber is
// currently complained, EVERY column this method would otherwise touch —
// including status — is left unchanged and no error is returned. This is
// the store-layer half of closing the laundering path #0025's review
// predicted for this exact method: even if a caller's own status check
// races with a concurrent complaint (SES notification landing between the
// handler's read and this write), the UPDATE itself cannot move a
// complained row to pending. The #0026 handler additionally never calls this
// method at all when its own read of status was complained (it answers 202
// with nothing sent instead) — this guard is the defense-in-depth backstop
// for the TOCTOU window, not the only line of defense.
func (s *Store) RestartSignup(ctx context.Context, id int64, in RestartSignupInput, now time.Time) (Subscriber, error) {
	confirmToken, err := newToken()
	if err != nil {
		return Subscriber{}, err
	}
	confirmExpiresAt := now.Add(in.ConfirmTTL)

	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET status             = CASE WHEN status = $11 THEN status             ELSE $2    END,
		        confirm_token      = CASE WHEN status = $11 THEN confirm_token      ELSE $3    END,
		        confirm_sent_at    = CASE WHEN status = $11 THEN confirm_sent_at    ELSE NULL  END,
		        confirm_expires_at = CASE WHEN status = $11 THEN confirm_expires_at ELSE $4    END,
		        confirmed_at       = CASE WHEN status = $11 THEN confirmed_at       ELSE NULL  END,
		        signup_ip          = CASE WHEN status = $11 THEN signup_ip          ELSE $5    END,
		        signup_user_agent  = CASE WHEN status = $11 THEN signup_user_agent  ELSE $6    END,
		        utm_source         = CASE WHEN status = $11 THEN utm_source         ELSE $7    END,
		        utm_medium         = CASE WHEN status = $11 THEN utm_medium         ELSE $8    END,
		        utm_campaign       = CASE WHEN status = $11 THEN utm_campaign       ELSE $9    END,
		        updated_at         = CASE WHEN status = $11 THEN updated_at         ELSE $10   END
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, StatusPending, confirmToken, confirmExpiresAt,
		nullIfEmpty(in.SignupIP), nullIfEmpty(in.SignupUserAgent),
		nullIfEmpty(in.UTMSource), nullIfEmpty(in.UTMMedium), nullIfEmpty(in.UTMCampaign),
		now, statusLockedFromNonAdmin,
	)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: restarting signup for %d: %w", id, err)
	}
	return sub, nil
}

// SetInterests replaces a subscriber's full set of interest associations with
// interestIDs in a single transaction (delete-then-insert), so a partial
// write can never leave a stale association behind. An empty or nil
// interestIDs is valid and expected (PRD §6.1): the subscriber ends up with
// zero interests and receives general announcements only.
func (s *Store) SetInterests(ctx context.Context, subscriberID int64, interestIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("subscribers: beginning set-interests tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM subscriber_interests WHERE subscriber_id = $1`, subscriberID,
	); err != nil {
		return fmt.Errorf("subscribers: clearing interests for %d: %w", subscriberID, err)
	}

	for _, interestID := range interestIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO subscriber_interests (subscriber_id, interest_id)
			 VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`,
			subscriberID, interestID,
		); err != nil {
			return fmt.Errorf("subscribers: linking interest %d to subscriber %d: %w", interestID, subscriberID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("subscribers: committing set-interests tx: %w", err)
	}
	return nil
}

// InterestIDs returns the ids of every interest a subscriber has selected,
// ordered by id. Empty (not an error) when the subscriber has zero
// interests.
func (s *Store) InterestIDs(ctx context.Context, subscriberID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT interest_id FROM subscriber_interests
		  WHERE subscriber_id = $1
		  ORDER BY interest_id`, subscriberID)
	if err != nil {
		return nil, fmt.Errorf("subscribers: listing interests for %d: %w", subscriberID, err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("subscribers: scanning interest id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating interest ids: %w", err)
	}
	return out, nil
}

// MarkBounced transitions a subscriber to bounced. Intended for the SES
// bounce-notification handling landing in a later issue; included here so
// the status vocabulary in this package is complete from the start.
//
// If the subscriber is currently complained, this is a silent no-op (see
// setStatus): a bounce notification arriving for an already-complained
// address must not erase the complaint, and — like Unsubscribe — the SES
// notification handler calling this has a webhook contract to satisfy
// (acknowledge promptly) rather than a user it can show an error to.
func (s *Store) MarkBounced(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	return s.setStatus(ctx, id, StatusBounced, now)
}

// MarkComplained transitions a subscriber to complained. Calling it again on
// an already-complained subscriber is a harmless no-op (setStatus's guard
// only ever preserves complained, and complained is what it's already
// setting). Nothing in this package other than AdminClearComplaint ever
// transitions a subscriber back out of complained — per CLAUDE.md §9, only
// an admin clears that state.
func (s *Store) MarkComplained(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	return s.setStatus(ctx, id, StatusComplained, now)
}

// setStatus is the shared implementation behind MarkBounced and
// MarkComplained. It guards statusLockedFromNonAdmin the same way
// Unsubscribe does: if the subscriber is currently complained, the UPDATE
// preserves status and updated_at rather than overwriting them, and no
// error is returned — this method's two callers are both webhook-driven
// (SES bounce/complaint notifications), not code with a user to report an
// error to. AdminClearComplaint is the only exception to this guard.
func (s *Store) setStatus(ctx context.Context, id int64, status string, now time.Time) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET status     = CASE WHEN status = $4 THEN status     ELSE $2 END,
		        updated_at = CASE WHEN status = $4 THEN updated_at ELSE $3 END
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, status, now, statusLockedFromNonAdmin,
	)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: setting status of %d to %q: %w", id, status, err)
	}
	return sub, nil
}

// findByID looks up a subscriber by primary key. Unexported: it exists to
// let AdminClearComplaint distinguish "no such subscriber" from "subscriber
// exists but isn't complained" without a second exported lookup method that
// nothing else needs yet.
func (s *Store) findByID(ctx context.Context, id int64) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE id = $1`, id)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: finding by id: %w", err)
	}
	return sub, nil
}

// AdminClearComplaint is the one method in this package allowed to move a
// subscriber out of the complained status (CLAUDE.md §9: "only an admin
// clears that state"). It is deliberately the single exception to
// statusLockedFromNonAdmin rather than a parameter on an existing method,
// so every other call site stays impossible to abuse into an
// auto-resubscribe: nothing about Unsubscribe or MarkBounced's signature
// offers a way to bypass the guard, and the only method that can is named
// for exactly what it does and lives here, next to the guard it deliberately
// skips.
//
// It requires the subscriber to currently be complained (ErrNotComplained
// otherwise) — this is an operator-driven action from #0032's admin screen,
// which only offers the action when the row is complained, so a caller
// hitting this in the wrong state is a bug worth surfacing as an error, not
// a protocol the store must paper over.
//
// The resulting status is unsubscribed, not active: clearing a complaint
// removes the block on the address ever resubscribing, but it does not by
// itself re-establish the double opt-in consent PRD §6.3 requires before
// mail resumes. Records the action the same way a manual unsubscribe would
// (unsubscribed_at, unsubscribe_source=admin) since the schema has no
// separate "complaint cleared" column and this reuses an existing,
// already-audited pair of fields rather than adding one. #0032 can choose a
// different resulting status later; this is the conservative default.
func (s *Store) AdminClearComplaint(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET status = $2, unsubscribed_at = $3, unsubscribe_source = $4, updated_at = $3
		  WHERE id = $1 AND status = $5
		 RETURNING `+subscriberColumns,
		id, StatusUnsubscribed, now, SourceAdmin, StatusComplained,
	)
	sub, err := scanSubscriber(row)
	if errors.Is(err, pgx.ErrNoRows) {
		_, ferr := s.findByID(ctx, id)
		if errors.Is(ferr, ErrNotFound) {
			return Subscriber{}, ErrNotFound
		}
		if ferr != nil {
			return Subscriber{}, ferr
		}
		return Subscriber{}, ErrNotComplained
	}
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: admin clearing complaint for %d: %w", id, err)
	}
	return sub, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate email or token collision.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

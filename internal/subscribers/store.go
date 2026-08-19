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
// complained never auto-resubscribes (CLAUDE.md §9): Confirm refuses to
// move a complained subscriber to active. No method in this package moves a
// subscriber out of the complained status — that is an admin-only action
// reserved for a future issue (#0032), deliberately not built here.
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
		     ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		 RETURNING `+subscriberColumns,
		email, StatusPending, confirmToken, now, confirmExpiresAt,
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
// to call on an already-unsubscribed, bounced, or complained row — it only
// ever moves a subscriber further from "active", never back toward it, so it
// can't be used to circumvent the complained lock in Confirm.
func (s *Store) Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET status = $2, unsubscribed_at = $3, unsubscribe_source = $4, updated_at = $3
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, StatusUnsubscribed, now, source,
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
func (s *Store) MarkBounced(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	return s.setStatus(ctx, id, StatusBounced, now)
}

// MarkComplained transitions a subscriber to complained. Nothing in this
// package ever transitions a subscriber back out of complained — per
// CLAUDE.md §9, only an admin (a future issue's admin-only code path) clears
// that state.
func (s *Store) MarkComplained(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	return s.setStatus(ctx, id, StatusComplained, now)
}

func (s *Store) setStatus(ctx context.Context, id int64, status string, now time.Time) (Subscriber, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers SET status = $2, updated_at = $3
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, status, now,
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

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate email or token collision.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Package subscribers is the data-access layer for the mailing list (PRD
// §6.2, §6.3): the subscribers table (list membership independent of any
// user account, plus the consent evidence that makes the list defensible)
// and the subscriber_interests join table.
//
// Email normalization: every write path normalizes the address in SQL —
// lower(trim($1)) — rather than in Go, per #0026's review (finding, "narrow
// the non-ASCII rejection"). An earlier version lowercased in Go
// (strings.ToLower) before handing the result to the database, which
// disagrees with Postgres's lower() on at least one titlecase-digraph
// codepoint (ǅ U+01C5): Go folds it to ǆ, Postgres leaves it unchanged, so
// a value the Go layer had already "normalized" could still trip the
// subscribers_email_normalized CHECK below. Normalizing in SQL means the
// same engine that enforces the CHECK also computes the value stored, so
// the two can never disagree — the CHECK is satisfied by construction, for
// every codepoint, not just ASCII. This is also why the #0026 handler no
// longer rejects non-ASCII email syntax: RFC 6531 (SMTPUTF8) addresses are
// legitimate and the divergence that justified the restriction no longer
// exists. Per the issue's notes, Gmail dots and "+tag" suffixes are never
// stripped — they are distinct addresses per RFC 5321/5322 and people use
// them deliberately to segment their own mail.
//
// Tokens: confirm_token and manage_token are 32 random bytes from
// crypto/rand, base64url-encoded (tokens.go) — not an HMAC of the email
// (PRD §6.4). Random tokens are individually revocable by rotating the
// column and leak nothing if a URL ends up in a referrer header or a
// screenshot.
//
// complained never auto-resubscribes (CLAUDE.md §9): every status mutator in
// this package — Confirm, Unsubscribe, MarkBounced(Tx), MarkComplained(Tx)
// (via the shared setStatusTx), and RestartSignup — refuses to move a
// subscriber out of the complained status. The check lives in one place
// (statusLockedFromNonAdmin, used by setStatusTx and by Unsubscribe's and
// RestartSignup's own UPDATEs) so a future mutator can't add a new unguarded
// status write the way an earlier version of this file did. MarkBouncedTx
// and MarkComplainedTx (#0038, SES event ingestion) run the identical
// guarded UPDATE against a caller-supplied querier instead of the pool, so
// even a transaction-scoped caller can't bypass it.
// AdminClearComplaint is the sole exception: it is the admin-only path
// (#0032) allowed to move a subscriber out of complained, and it is the only
// method that does not consult the guard.
//
// synthetic (added by migration 000019, #0046's review): true only for the
// per-admin test-send recipient rows ensureTestRecipient
// (internal/handlers/admin_campaign_preview.go) finds-or-creates. It is
// consulted in exactly two places — StatusCounts and List, both of which
// unconditionally exclude synthetic=true so a QA fixture can never inflate
// the #0032 admin screen's counts or appear in its subscriber table — and
// nowhere else. In particular it is NOT consulted by audienceWhere
// (internal/mailing), which already excludes every non-'active' row by its
// own status equality, nor by FindByManageToken/Unsubscribe, since a test
// message's unsubscribe link must keep working exactly like a real one
// (this package's whole reason for anchoring the token to a real row at
// all). See IsReservedTestEmail below for the companion guard that keeps
// the public subscribe endpoint from ever mailing a synthetic address's
// deterministic, publicly-known domain in the first place.
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

// ReservedTestEmailDomain is the RFC 2606-reserved domain
// #0046's ensureTestRecipient anchors every synthetic test-send recipient
// row to (internal/handlers/admin_campaign_preview.go): guaranteed to never
// resolve, so any attempt to actually deliver mail to an address at this
// domain is a certain hard bounce. The single source of truth for that
// domain string — see IsReservedTestEmail.
const ReservedTestEmailDomain = "internal.opencircuitsf.test"

// IsReservedTestEmail reports whether email's domain is
// ReservedTestEmailDomain, matched case-insensitively and after trimming
// surrounding whitespace to mirror this package's own lower(trim($1))
// normalization (see the package doc comment). #0046's review (finding B):
// the deterministic address format
// (campaign-test+admin-<id>@internal.opencircuitsf.test) is public in this
// package's own source, and admin ids are small, trivially enumerable
// integers, so the public subscribe endpoint (internal/handlers/
// subscribe.go) must refuse to mail ANY address at this domain — not only
// ones a synthetic row already exists for — or an unauthenticated caller
// could walk admin ids and trigger a guaranteed hard bounce against SES's
// sender reputation on demand.
func IsReservedTestEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	suffix := "@" + ReservedTestEmailDomain
	return strings.HasSuffix(email, suffix) && len(email) > len(suffix)
}

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
	ID               int64
	Email            string
	Status           string
	ConfirmToken     *string
	ConfirmSentAt    *time.Time
	ConfirmExpiresAt *time.Time
	ConfirmedAt      *time.Time
	// AlreadySubscribedSentAt is the claim column for the "you're already
	// subscribed" email (#0026 review finding 1): mirrors ConfirmSentAt's
	// role for the confirmation email, gating the same once-per-cooldown
	// send via ClaimAlreadySubscribedSend / ReleaseAlreadySubscribedClaim.
	AlreadySubscribedSentAt *time.Time
	ManageToken             string
	SignupIP                *string
	SignupUserAgent         *string
	UTMSource               *string
	UTMMedium               *string
	UTMCampaign             *string
	UnsubscribedAt          *time.Time
	UnsubscribeSource       *string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	// Synthetic is migration 000019's flag — see the package doc comment.
	// True only for #0046's per-admin test-send recipient rows.
	Synthetic bool
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
	// Synthetic marks the created row migration 000019's synthetic column
	// (see the package doc comment). Every real caller — the public
	// subscribe endpoint (#0026) and the admin manual-add flow (#0032) —
	// leaves this false; only #0046's ensureTestRecipient sets it true.
	Synthetic bool
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

const subscriberColumns = `id, email, status, confirm_token, confirm_sent_at,
	confirm_expires_at, confirmed_at, already_subscribed_sent_at, manage_token,
	host(signup_ip), signup_user_agent, utm_source, utm_medium, utm_campaign,
	unsubscribed_at, unsubscribe_source, created_at, updated_at, synthetic`

func scanSubscriber(row pgx.Row) (Subscriber, error) {
	var sub Subscriber
	err := row.Scan(
		&sub.ID, &sub.Email, &sub.Status, &sub.ConfirmToken, &sub.ConfirmSentAt,
		&sub.ConfirmExpiresAt, &sub.ConfirmedAt, &sub.AlreadySubscribedSentAt, &sub.ManageToken,
		&sub.SignupIP, &sub.SignupUserAgent, &sub.UTMSource, &sub.UTMMedium, &sub.UTMCampaign,
		&sub.UnsubscribedAt, &sub.UnsubscribeSource, &sub.CreatedAt, &sub.UpdatedAt, &sub.Synthetic,
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
	confirmToken, err := newToken()
	if err != nil {
		return Subscriber{}, err
	}
	manageToken, err := newToken()
	if err != nil {
		return Subscriber{}, err
	}
	confirmExpiresAt := now.Add(in.ConfirmTTL)

	// email is normalized in SQL (lower(trim($1))), not in Go — see the
	// package doc comment on why: one engine defines normalization, so it
	// can never disagree with the subscribers_email_normalized CHECK below.
	row := s.pool.QueryRow(ctx,
		`INSERT INTO subscribers
		     (email, status, confirm_token, confirm_sent_at, confirm_expires_at,
		      manage_token, signup_ip, signup_user_agent,
		      utm_source, utm_medium, utm_campaign, created_at, updated_at, synthetic)
		 VALUES
		     (lower(trim($1)), $2, $3, NULL, $4, $5, $6, $7, $8, $9, $10, $11, $11, $12)
		 RETURNING `+subscriberColumns,
		in.Email, StatusPending, confirmToken, confirmExpiresAt,
		manageToken, nullIfEmpty(in.SignupIP), nullIfEmpty(in.SignupUserAgent),
		nullIfEmpty(in.UTMSource), nullIfEmpty(in.UTMMedium), nullIfEmpty(in.UTMCampaign), now,
		in.Synthetic,
	)
	sub, err := scanSubscriber(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Subscriber{}, ErrEmailExists
		}
		return Subscriber{}, fmt.Errorf("subscribers: creating %q: %w", in.Email, err)
	}
	return sub, nil
}

// ClaimConfirmationSend atomically claims the right to send a confirmation
// email to subscriber id: it stamps confirm_sent_at = now in the same
// UPDATE that checks the once-per-hour cooldown (PRD §6.3), so the check
// and the claim can never be split by a race the way a separate read-then-
// write would be.
//
// Returns claimed=true (and stamps the row) only if confirm_sent_at is
// currently NULL or older than now-cooldown; otherwise it changes nothing
// and returns claimed=false — either because the cooldown is genuinely
// active, or because a concurrent request already won the claim a moment
// ago. The caller cannot tell those two cases apart from this return value
// alone, and does not need to: either way, this request must not send.
//
// #0026's review (finding 3) found the earlier MarkConfirmationSent — an
// unconditional stamp, called only after a successful send, with the
// cooldown decision made by a separate, earlier read of the row — left a
// window between that read and the eventual write where multiple
// concurrent requests could all observe "not in cooldown" and all send.
// Folding the check into the UPDATE's WHERE clause closes that window: at
// most one concurrent caller can ever see claimed=true for a given
// cooldown period. See ReleaseConfirmationClaim for what happens if the
// send this claim was for then fails.
func (s *Store) ClaimConfirmationSend(ctx context.Context, id int64, now time.Time, cooldown time.Duration) (claimed bool, err error) {
	cutoff := now.Add(-cooldown)
	tag, err := s.pool.Exec(ctx,
		`UPDATE subscribers
		    SET confirm_sent_at = $2, updated_at = $2
		  WHERE id = $1 AND (confirm_sent_at IS NULL OR confirm_sent_at < $3)`,
		id, now, cutoff,
	)
	if err != nil {
		return false, fmt.Errorf("subscribers: claiming confirmation send for %d: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseConfirmationClaim reverts a ClaimConfirmationSend claim whose send
// then failed, so a later request can retry immediately rather than being
// blocked by a cooldown anchored to a message that was never delivered —
// the same "don't get stuck for an hour" property #0025's review required
// of the original stamp-after-send design, preserved here despite the
// stamp now happening before the send.
//
// Sets confirm_sent_at back to NULL unconditionally, not to whatever value
// preceded the claim. That is provably equivalent for this column's only
// purpose (gating the cooldown): ClaimConfirmationSend's WHERE clause
// guarantees the prior value was either already NULL or already more than
// cooldown old, and both of those states permit an immediate resend, which
// is exactly what NULL also permits. No caller of this method needs to
// distinguish those two "already permits a resend" states from each other.
//
// Deliberately does NOT consult statusLockedFromNonAdmin, for the same
// reason MarkConfirmationSent didn't: this method never writes status.
func (s *Store) ReleaseConfirmationClaim(ctx context.Context, id int64, now time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE subscribers SET confirm_sent_at = NULL, updated_at = $2 WHERE id = $1`,
		id, now,
	); err != nil {
		return fmt.Errorf("subscribers: releasing confirmation claim for %d: %w", id, err)
	}
	return nil
}

// ClaimAlreadySubscribedSend is ClaimConfirmationSend's counterpart for the
// "you're already subscribed" email (PRD §6.3's active branch). #0026's
// review (finding 1) measured 20 sequential submits of one active
// subscriber's address producing 20 emails to that person — this path had
// no cooldown of any kind, unlike the confirmation email — which is both an
// unauthenticated mail-amplification vector and half of a two-probe
// enumeration oracle (see internal/handlers/subscribe.go's package doc
// comment). Same atomic claim-in-the-WHERE-clause shape, same reasoning,
// a separate column (already_subscribed_sent_at) because the two emails
// have independent cooldowns.
func (s *Store) ClaimAlreadySubscribedSend(ctx context.Context, id int64, now time.Time, cooldown time.Duration) (claimed bool, err error) {
	cutoff := now.Add(-cooldown)
	tag, err := s.pool.Exec(ctx,
		`UPDATE subscribers
		    SET already_subscribed_sent_at = $2, updated_at = $2
		  WHERE id = $1 AND (already_subscribed_sent_at IS NULL OR already_subscribed_sent_at < $3)`,
		id, now, cutoff,
	)
	if err != nil {
		return false, fmt.Errorf("subscribers: claiming already-subscribed send for %d: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseAlreadySubscribedClaim is ReleaseConfirmationClaim's counterpart
// for already_subscribed_sent_at; see that method's doc comment for why
// reverting to NULL (rather than the pre-claim value) is correct.
func (s *Store) ReleaseAlreadySubscribedClaim(ctx context.Context, id int64, now time.Time) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE subscribers SET already_subscribed_sent_at = NULL, updated_at = $2 WHERE id = $1`,
		id, now,
	); err != nil {
		return fmt.Errorf("subscribers: releasing already-subscribed claim for %d: %w", id, err)
	}
	return nil
}

// FindByEmail looks up a subscriber by (normalized) email through the
// shared pool. Normalizes in SQL (lower(trim($1))), matching Create — see
// the package doc comment. See FindByEmailTx for the transaction-scoped
// twin.
func (s *Store) FindByEmail(ctx context.Context, email string) (Subscriber, error) {
	return s.findByEmail(ctx, s.pool, email)
}

// FindByEmailTx is FindByEmail's transaction-scoped twin: it runs the
// identical SELECT against q instead of the pool, so a caller that already
// holds an open pgx.Tx can look a recipient up on the SAME checked-out
// connection instead of asking the pool for a second one while the first is
// still held open by that transaction. #0038's handler
// (internal/handlers/ses_notifications.go, applyRecipient) is the first
// caller: /api/ses/notifications deliberately carries no rate limiter (a
// campaign produces thousands of events in a burst from many source IPs),
// so under a burst every request holding a transaction open would
// previously also compete with every other one for a second pooled
// connection just to run this lookup — every connection could end up held
// by an open transaction with every request blocked waiting for one more,
// with nothing recovering until contexts cancel. Reading through q also
// makes the lookup see the transaction's own uncommitted writes, which
// #0039 will want.
func (s *Store) FindByEmailTx(ctx context.Context, q querier, email string) (Subscriber, error) {
	return s.findByEmail(ctx, q, email)
}

func (s *Store) findByEmail(ctx context.Context, q querier, email string) (Subscriber, error) {
	row := q.QueryRow(ctx,
		`SELECT `+subscriberColumns+` FROM subscribers WHERE email = lower(trim($1))`,
		email)
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

// FindByManageToken looks up a subscriber by its manage_token. The token
// carries no expiry timestamp of its own, but it is not immutable either:
// #0034's one-click unsubscribe handler calls RotateManageToken after a
// real (non-no-op) unsubscribe, so a replay of an already-consumed footer
// link stops resolving here. (An earlier version of this comment asserted
// "long-lived; no expiry" as if that meant the value never changes — #0025's
// review named exactly this pattern, a doc comment asserting an untrue
// invariant, as what let a laundering bug survive review; #0034 carried the
// same finding forward against this line specifically.) Backs the
// preference center (#0031, which deliberately never rotates — see
// RotateManageToken's doc comment for why the two differ) and one-click
// unsubscribe (#0034).
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
//
// The returned row is the authoritative answer to "did this actually
// change anything": it reflects what the guarded UPDATE above did,
// atomically, not what the caller believed going in. A caller that instead
// compares a status it read before calling Unsubscribe can be wrong if a
// complaint lands between that read and this call — #0104, found in both
// callers this package had at the time (UnsubscribeHandler.Post,
// PreferencesHandler.patchUnsubscribe), which both derived their no-op
// decision from a pre-call read until that issue fixed them to use
// updated.Status here instead. Compare the returned Subscriber's Status
// against StatusComplained (or whatever else matters) rather than reaching
// for a value read earlier in the request.
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

// RotateManageToken generates a fresh manage_token for subscriber id and
// stores it, invalidating every link (preference-center or one-click footer)
// that carried the previous value. #0034's one-click unsubscribe handler is
// the one call site: it rotates after a REAL (non-no-op) unsubscribe, so a
// replayed POST of the same already-consumed footer link no longer resolves
// via FindByManageToken and gets the endpoint's neutral "unknown token"
// response instead of re-running any mutation. The preference center
// (#0031) deliberately never calls this — see FindByManageToken's doc
// comment for why the two paths differ.
//
// Guards statusLockedFromNonAdmin the same way every other mutator in this
// package does (see the package doc comment): on a currently-complained row
// this is a silent no-op — manage_token and updated_at are left unchanged,
// no error is returned. This is deliberate, not incidental: rotation's
// actual purpose is stopping replay of a CONSUMED unsubscribe link, and that
// has no target on a row that is already terminal and refused by every
// other mutator — rotating there would instead churn the token on every
// unattended hit from a mail provider's automated retry/prefetch
// infrastructure, invalidating every live footer link the person holds as
// the price of an action that changed nothing. UnsubscribeHandler (#0034)
// already checks the pre-call status itself (mirroring
// PreferencesHandler.patchUnsubscribe's `before := sub.Status`) and skips
// calling this method at all in that case; this guard is a second,
// independent line of defense for any future call site, not the only one.
func (s *Store) RotateManageToken(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	token, err := newToken()
	if err != nil {
		return Subscriber{}, err
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE subscribers
		    SET manage_token = CASE WHEN status = $4 THEN manage_token ELSE $2 END,
		        updated_at   = CASE WHEN status = $4 THEN updated_at   ELSE $3 END
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, token, now, statusLockedFromNonAdmin,
	)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: rotating manage token for %d: %w", id, err)
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

// MarkBounced transitions a subscriber to bounced through the shared pool.
// See setStatusTx for the guard this and every sibling below shares.
func (s *Store) MarkBounced(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	return s.setStatusTx(ctx, s.pool, id, StatusBounced, now)
}

// MarkBouncedTx is MarkBounced's transaction-scoped twin: it runs the
// identical UPDATE against q instead of the pool, so #0038 (SES event
// ingestion) can commit or roll back this status change atomically with its
// own email_events insert and suppressions.AddTx call for the same incoming
// SNS message — see internal/subscribers/suppressions.go's package doc
// comment for why that atomicity matters, and
// internal/handlers/ses_notifications.go for the caller.
func (s *Store) MarkBouncedTx(ctx context.Context, q querier, id int64, now time.Time) (Subscriber, error) {
	return s.setStatusTx(ctx, q, id, StatusBounced, now)
}

// MarkComplained transitions a subscriber to complained through the shared
// pool. Calling it again on an already-complained subscriber is a harmless
// no-op (setStatusTx's guard only ever preserves complained, and complained
// is what it's already setting). Nothing in this package other than
// AdminClearComplaint ever transitions a subscriber back out of complained —
// per CLAUDE.md §9, only an admin clears that state.
func (s *Store) MarkComplained(ctx context.Context, id int64, now time.Time) (Subscriber, error) {
	return s.setStatusTx(ctx, s.pool, id, StatusComplained, now)
}

// MarkComplainedTx is MarkComplained's transaction-scoped twin — see
// MarkBouncedTx's doc comment; the same #0038 atomicity requirement applies
// identically to a complaint as to a bounce.
func (s *Store) MarkComplainedTx(ctx context.Context, q querier, id int64, now time.Time) (Subscriber, error) {
	return s.setStatusTx(ctx, q, id, StatusComplained, now)
}

// setStatusTx is the shared implementation behind MarkBounced(Tx) and
// MarkComplained(Tx), run against q so a caller that already owns an open
// pgx.Tx (#0038) can make this status change atomic with the rest of its
// work — the same querier/…Tx split internal/audit's WriteTx and this
// package's own SuppressionStore.AddTx already establish, for the identical
// reason (see suppressions.go's package doc comment). It guards
// statusLockedFromNonAdmin the same way Unsubscribe does: if the subscriber
// is currently complained, the UPDATE preserves status and updated_at
// rather than overwriting them, and no error is returned — every caller of
// this method is webhook-driven (SES bounce/complaint notifications), not
// code with a user it can show an error to. The guard lives here, in the
// ONE shared implementation, rather than being duplicated across
// MarkBounced/MarkBouncedTx/MarkComplained/MarkComplainedTx, so a Tx-taking
// caller can never bypass it by construction. AdminClearComplaint is the
// only exception to this guard anywhere in the package.
func (s *Store) setStatusTx(ctx context.Context, q querier, id int64, status string, now time.Time) (Subscriber, error) {
	row := q.QueryRow(ctx,
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

// GetByID looks up a subscriber by primary key. Exported for #0032's admin
// screen (subscriber detail view, and the before/after reads Suppress and
// ClearComplaint need to tell an admin whether their action actually took
// effect — see internal/handlers/admin_subscribers.go). Originally
// unexported (findByID) and used only by AdminClearComplaint to distinguish
// "no such subscriber" from "subscriber exists but isn't complained"; #0032
// needed the same lookup from outside this package, so it was promoted
// rather than duplicated.
func (s *Store) GetByID(ctx context.Context, id int64) (Subscriber, error) {
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
		_, ferr := s.GetByID(ctx, id)
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

// ── Admin list/search (#0032) ────────────────────────────────────────────────

// subscribersListDefaultPerPage and subscribersListMaxPerPage bound List's
// page size: a caller-supplied PerPage <1 falls back to the default, and
// anything above the max is clamped down to it, so a malformed or hostile
// ?per_page= query parameter can never force an unbounded scan.
const (
	subscribersListDefaultPerPage = 25
	subscribersListMaxPerPage     = 200
)

// ListFilter narrows List's result set. Every field is optional: the zero
// value (empty Status/Query, InterestID 0, Page/PerPage <1) matches every
// subscriber, defaults the page size, and starts at page 1.
type ListFilter struct {
	Status     string // one of the Status* constants; "" matches any status
	InterestID int64  // 0 matches any; otherwise restricts to subscribers with this interest selected
	Query      string // substring match against email, case-insensitive; "" matches any
	Page       int    // 1-based; <1 is treated as 1
	PerPage    int    // <1 falls back to subscribersListDefaultPerPage; clamped to subscribersListMaxPerPage
}

// escapeLikeSpecials backslash-escapes the two ILIKE wildcard characters (%
// and _) and the escape character itself in a user-supplied search string, so
// a search for e.g. "50%off@example.com" or "j_doe@example.com" matches that
// literal substring rather than being interpreted as a pattern. Paired with
// `ESCAPE '\'` in List's query.
func escapeLikeSpecials(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// List returns subscribers matching filter, newest-first, alongside the
// total number of rows the filter matches (ignoring Page/PerPage) so the
// caller (the #0032 admin screen) can render pagination controls. Every
// selected column is qualified with the subscribers. table prefix because
// filtering by InterestID joins subscriber_interests, whose own created_at
// column would otherwise collide with subscribers.created_at in an
// unqualified SELECT.
//
// The InterestID join can never multiply a subscriber's row: subscriber_
// interests' primary key is (subscriber_id, interest_id), so filtering to
// one specific interest_id matches at most one join row per subscriber.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Subscriber, int64, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	perPage := filter.PerPage
	if perPage < 1 {
		perPage = subscribersListDefaultPerPage
	}
	if perPage > subscribersListMaxPerPage {
		perPage = subscribersListMaxPerPage
	}

	var (
		joins []string
		// synthetic = false is unconditional, not part of ListFilter: the
		// #0032 admin screen this method serves must never show a #0046
		// test-send fixture regardless of what status/interest/query the
		// caller filters by — see the package doc comment on the synthetic
		// column. Individual-row lookups (FindByEmail, FindByManageToken,
		// GetByID) deliberately do NOT apply this exclusion; only the
		// aggregate views (List, StatusCounts) do.
		where = []string{`subscribers.synthetic = false`}
		args  []any
	)
	if filter.InterestID != 0 {
		joins = append(joins, `JOIN subscriber_interests si ON si.subscriber_id = subscribers.id`)
		args = append(args, filter.InterestID)
		where = append(where, fmt.Sprintf(`si.interest_id = $%d`, len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf(`subscribers.status = $%d`, len(args)))
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		args = append(args, "%"+escapeLikeSpecials(q)+"%")
		where = append(where, fmt.Sprintf(`subscribers.email ILIKE $%d ESCAPE '\'`, len(args)))
	}

	joinSQL := strings.Join(joins, " ")
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	countSQL := fmt.Sprintf(`SELECT count(*) FROM subscribers %s %s`, joinSQL, whereSQL)
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("subscribers: counting list: %w", err)
	}
	if total == 0 {
		return []Subscriber{}, 0, nil
	}

	listArgs := append(append([]any{}, args...), perPage, (page-1)*perPage)
	listSQL := fmt.Sprintf(
		`SELECT %s FROM subscribers %s %s ORDER BY subscribers.created_at DESC, subscribers.id DESC LIMIT $%d OFFSET $%d`,
		qualifiedSubscriberColumns, joinSQL, whereSQL, len(listArgs)-1, len(listArgs),
	)
	rows, err := s.pool.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("subscribers: listing: %w", err)
	}
	defer rows.Close()

	out := make([]Subscriber, 0, perPage)
	for rows.Next() {
		sub, err := scanSubscriber(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("subscribers: scanning list row: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("subscribers: iterating list: %w", err)
	}
	return out, total, nil
}

// qualifiedSubscriberColumns is subscriberColumns with every column prefixed
// by the subscribers. table name, for queries (List) that JOIN another
// table and would otherwise risk an ambiguous or wrongly-resolved unqualified
// column reference (subscriber_interests.created_at vs
// subscribers.created_at).
const qualifiedSubscriberColumns = `subscribers.id, subscribers.email, subscribers.status,
	subscribers.confirm_token, subscribers.confirm_sent_at, subscribers.confirm_expires_at,
	subscribers.confirmed_at, subscribers.already_subscribed_sent_at, subscribers.manage_token,
	host(subscribers.signup_ip), subscribers.signup_user_agent, subscribers.utm_source,
	subscribers.utm_medium, subscribers.utm_campaign, subscribers.unsubscribed_at,
	subscribers.unsubscribe_source, subscribers.created_at, subscribers.updated_at,
	subscribers.synthetic`

// StatusCounts returns the number of subscribers in each status, keyed by
// the Status* constants. Every known status is present in the result even
// when its count is zero, so the #0032 admin screen's "counts by status"
// header always shows all five rather than silently omitting one with no
// members yet.
//
// Excludes synthetic=true rows unconditionally, same as List — see the
// package doc comment. Without this, #0046's review found every admin who
// has ever run a campaign test send permanently inflates the "pending"
// bucket by one, telling the operator someone is mid-double-opt-in who is
// actually a QA fixture.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{
		StatusPending:      0,
		StatusActive:       0,
		StatusUnsubscribed: 0,
		StatusBounced:      0,
		StatusComplained:   0,
	}
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM subscribers WHERE synthetic = false GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("subscribers: counting by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("subscribers: scanning status count: %w", err)
		}
		counts[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating status counts: %w", err)
	}
	return counts, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate email or token collision.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

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

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
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

// Provenance values (#0125, PRD §6.10), matching the subscribers_source_check
// and subscribers_consent_basis_check CHECK constraints (migrations/000010).
// Named Subscriber* rather than reusing Source*/Status*-shaped names to keep
// this column's vocabulary (where an ADDRESS entered the list) unmistakably
// separate from unsubscribe_source's (why it LEFT) — the two are easy to
// conflate since both are literally named "source" at the database.
const (
	SubscriberSourceSignupForm  = "signup_form"
	SubscriberSourceImport      = "import"
	SubscriberSourceAdminManual = "admin_manual"
	SubscriberSourceAPI         = "api"
)

const (
	ConsentBasisDoubleOptIn          = "double_opt_in"
	ConsentBasisImportedPriorConsent = "imported_prior_consent"
	ConsentBasisAdminAttested        = "admin_attested"
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

// postEnqueueCommitHook, when non-nil, is called by Create and
// ClaimAndEnqueueConfirmation immediately after their own EnqueueTx call
// succeeds, still inside their transaction and before tx.Commit. Returning
// a non-nil error makes the caller return it, which — because the
// transaction has not yet committed — triggers the deferred tx.Rollback()
// and discards BOTH the subscribers-row mutation and the outbound_queue
// insert together.
//
// This is the mutation-proof seam for #0126's load-bearing property ("a
// committed signup can never have an unsent confirmation" — a crash between
// the insert and the enqueue is impossible because they share a
// transaction): a real crash landing between EnqueueTx and Commit is, from
// Postgres's point of view, indistinguishable from this hook returning an
// error at that exact point. See TestCreate_FailureAfterEnqueue_
// CommitsNeither and TestClaimAndEnqueueConfirmation_
// FailureAfterEnqueue_CommitsNeither (store_test.go) for the tests that use
// it, and #0126's phase-3 review (defect 3) for why one was required: a
// version of Create with the enqueue moved to run AFTER tx.Commit — the
// exact regression this issue forbids — left the pre-existing suite fully
// green, because nothing exercised the transaction boundary itself. Under
// that mutation this hook fires too late to roll anything back (the commit
// has already happened by the time the moved EnqueueTx call — and this
// hook right after it — runs), which is exactly how the tests tell the two
// apart. Always nil in production; matches internal/mailing/worker.go's
// sendPreCrashHook/claimAndDrainHook precedent for the identical reason.
var postEnqueueCommitHook func() error

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
	// Source, SourceDetail, ConsentBasis, ImportID, InvitedAt are #0125's
	// provenance columns (migrations/000010, PRD §6.10): every address must
	// be able to answer "where did this come from, and when?" without
	// reading subscriber_events. Source defaults to SourceSignupForm at the
	// database and is never left empty. InvitedAt is #0129's "one
	// invitation per address, ever" marker — always nil until that issue
	// lands a producer.
	//
	// # #0317 — Source and ImportID answer different questions, and may disagree
	//
	// Source/SourceDetail are HISTORICAL PROVENANCE: where this address
	// first entered the list, fixed at INSERT and never rewritten by any
	// mutator in this package (grep this file for `source\s*=` in an UPDATE
	// — there isn't one). ImportID is a CURRENT-STATE LINK: which
	// subscriber_imports batch, if any, this row's PRESENT subscription
	// still derives its consent from. RestartSignup clears ImportID (see
	// its own doc comment, "#0129") when a revoked-then-resigned-up row
	// starts a fresh, self-initiated signup — so a row can end up with
	// Source == SubscriberSourceImport and ImportID == nil at the same
	// time. That is not a bug: it means "this address originally came in
	// through an import, but its CURRENT subscription no longer derives
	// from that batch" — exactly the state PRD §6.10.1 already carves out
	// for a confirmed invitee, extended by #0129 to a resigned-up one.
	//
	// Do not re-link them — e.g. by having RestartSignup leave ImportID set,
	// or by having Revoke targeting key off Source instead of ImportID. That
	// re-introduces #0129's suppression misfire: a person who resigns up
	// after their invite was revoked would again be mistaken for a live,
	// unaccepted invitation and could be suppressed by a LATER Revoke of a
	// batch they no longer have any current relationship to. Nothing is
	// lost by leaving Source alone when ImportID clears — subscriber_events
	// keeps ImportID on the original `imported`/`invite_sent` rows, which is
	// where "why did we ever mail this address" is answered from (see
	// subscriberEventView / #0316 for surfacing that on the admin screen
	// after ImportID has been cleared here).
	//
	// #0311's imported_30d intentionally keys on Source (`source = 'import'
	// AND created_at >= since`), not ImportID — it is asking "did an import
	// batch ever place this address on the list", a historical-provenance
	// question, not "is this row still linked to that batch". The two
	// counts would disagree if imported_30d used ImportID instead: a
	// revoked-then-resigned-up row's original growth would silently vanish
	// from the historical figure the moment RestartSignup runs, which is
	// exactly the kind of "growth disappears without a corresponding
	// departure being counted" defect #0311 exists to close.
	//
	// A resigned-up former invitee (Source == SubscriberSourceImport,
	// ImportID == nil, ConsentBasis == ConsentBasisDoubleOptIn) IS worth
	// distinguishing from an ordinary website signup in the admin UI — an
	// operator asking "who actually came from that batch and is still
	// linked to it" gets that from ImportID directly; an operator asking
	// "who among today's active subscribers originally came in via an
	// import, whether or not they're still linked to it" needs Source. Both
	// questions are real and both fields already answer them; this issue
	// makes no UI change beyond #0316's event-level surfacing, since no
	// acceptance criterion asked for a new admin filter on this combination.
	Source       string
	SourceDetail *string
	ConsentBasis *string
	ImportID     *int64
	InvitedAt    *time.Time
	// SoftBounceStreak, LastBounceAt, LastDeliveryAt are #0124's delivery
	// health columns (migration 000010, PRD §6.9): the consecutive count of
	// Transient/Undetermined bounces since the last successful Delivery.
	// Zeroed by RecordDeliveryTx and by ResetSoftBounceStreakByEmail (a
	// suppression removed, or an admin's explicit reset) — see this file's
	// doc comment on those methods.
	SoftBounceStreak int
	LastBounceAt     *time.Time
	LastDeliveryAt   *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
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
	pool   *pgxpool.Pool
	outbox *outbox.Store
}

// NewStore constructs a Store over the shared connection pool. It also
// constructs its own *outbox.Store over the same pool (#0126) — internal/
// outbox is a leaf package with no dependency back on this one, so wiring
// it internally here, rather than threading a separate constructor
// parameter through every caller, keeps NewStore's signature unchanged for
// the many existing callers (subscribe.go, admin_campaign_preview.go, every
// test in this package and internal/handlers) while still giving Create/
// ClaimAndEnqueueConfirmation/ClaimAndEnqueueAlreadySubscribed the ability
// to enqueue inside their own transactions.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, outbox: outbox.NewStore(pool)}
}

// confirmationPayload is outbound_queue.payload's shape for
// outbox.KindConfirmation — template inputs, not rendered MIME (#0126's
// plan §2), so a template fix in internal/mailing applies to mail already
// queued. TTLSeconds is the NOMINAL ttl (the caller's ConfirmTTL, e.g. 7
// days), not a computed remaining-time-until-expiry — internal/handlers/
// subscribe.go's subscribeConfirmTTL doc comment explains why: a computed
// remaining time renders a ragged duration ("10079 minutes remaining")
// instead of a clean "7 days" for a resend.
type confirmationPayload struct {
	ConfirmToken string `json:"confirm_token"`
	ManageToken  string `json:"manage_token"`
	TTLSeconds   int64  `json:"ttl_seconds"`
}

// alreadySubscribedPayload is outbound_queue.payload's shape for
// outbox.KindAlreadySubscribed.
type alreadySubscribedPayload struct {
	ManageToken string `json:"manage_token"`
}

// welcomePayload is outbound_queue.payload's shape for outbox.KindWelcome
// (#0127) — mirrors internal/mailing.welcomePayload field-for-field (see
// internal/outbox's package doc comment: producer and consumer are matched
// by JSON field name, not a shared Go type, since they live in different
// packages and outbox.Item.Payload is deliberately `any`). InterestNames is
// captured HERE, inside Confirm's own transaction, not re-read by the
// worker at send time — see BuildWelcomeEmail's doc comment
// (internal/mailing/transactional_templates.go) for why a later preference
// change must not retroactively rewrite this one email.
type welcomePayload struct {
	ManageToken   string   `json:"manage_token"`
	InterestNames []string `json:"interest_names"`
}

const subscriberColumns = `id, email, status, confirm_token, confirm_sent_at,
	confirm_expires_at, confirmed_at, already_subscribed_sent_at, manage_token,
	host(signup_ip), signup_user_agent, utm_source, utm_medium, utm_campaign,
	unsubscribed_at, unsubscribe_source, source, source_detail, consent_basis,
	import_id, invited_at, soft_bounce_streak, last_bounce_at,
	last_delivery_at, created_at, updated_at, synthetic`

func scanSubscriber(row pgx.Row) (Subscriber, error) {
	var sub Subscriber
	err := row.Scan(
		&sub.ID, &sub.Email, &sub.Status, &sub.ConfirmToken, &sub.ConfirmSentAt,
		&sub.ConfirmExpiresAt, &sub.ConfirmedAt, &sub.AlreadySubscribedSentAt, &sub.ManageToken,
		&sub.SignupIP, &sub.SignupUserAgent, &sub.UTMSource, &sub.UTMMedium, &sub.UTMCampaign,
		&sub.UnsubscribedAt, &sub.UnsubscribeSource, &sub.Source, &sub.SourceDetail, &sub.ConsentBasis,
		&sub.ImportID, &sub.InvitedAt, &sub.SoftBounceStreak, &sub.LastBounceAt,
		&sub.LastDeliveryAt, &sub.CreatedAt, &sub.UpdatedAt, &sub.Synthetic,
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
//
// #0126: since #0126, Create ALSO claims the confirmation send and enqueues
// it on internal/outbox, and records the signup_requested event — all
// inside this same transaction, unless in.Synthetic is set (ensureTestRecipient's
// dedicated campaign-test recipient row, internal/handlers/
// admin_campaign_preview.go — that row's address is never meant to receive
// a real confirmation email, matching its behavior before #0126). A
// brand-new row has no cooldown to lose, so the claim here is
// unconditional — unlike ClaimAndEnqueueConfirmation, which guards a
// cooldown for an EXISTING row. This makes "a committed signup can never
// have an unsent confirmation" (this issue's load-bearing property)
// literally true for the new-signup path: Create either commits with its
// queue row, or neither exists.
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: beginning create tx for %q: %w", in.Email, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// email is normalized in SQL (lower(trim($1))), not in Go — see the
	// package doc comment on why: one engine defines normalization, so it
	// can never disagree with the subscribers_email_normalized CHECK below.
	row := tx.QueryRow(ctx,
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

	if !in.Synthetic {
		if _, err := tx.Exec(ctx,
			`UPDATE subscribers SET confirm_sent_at = $2, updated_at = $2 WHERE id = $1`,
			sub.ID, now,
		); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: claiming confirmation send for new signup %d: %w", sub.ID, err)
		}
		sub.ConfirmSentAt = &now

		if _, err := s.outbox.EnqueueTx(ctx, tx, outbox.Item{
			Kind:         outbox.KindConfirmation,
			Recipient:    sub.Email,
			SubscriberID: &sub.ID,
			Payload: confirmationPayload{
				ConfirmToken: confirmToken,
				ManageToken:  manageToken,
				TTLSeconds:   int64(in.ConfirmTTL.Seconds()),
			},
		}); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: enqueueing confirmation for new signup %d: %w", sub.ID, err)
		}

		// #0126 phase-3 review, defect 3 — see postEnqueueCommitHook's doc
		// comment: nil in production, a test-only seam right after the
		// enqueue and before commit.
		if postEnqueueCommitHook != nil {
			if err := postEnqueueCommitHook(); err != nil {
				return Subscriber{}, err
			}
		}

		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &sub.ID,
			Email:        sub.Email,
			Action:       ActionSignupRequested,
			Detail:       map[string]any{"kind": "new"},
		}); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: recording signup_requested for %d: %w", sub.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: committing create tx for %q: %w", in.Email, err)
	}
	return sub, nil
}

// ClaimAndEnqueueConfirmation atomically claims the right to send a
// confirmation email to subscriber id AND enqueues it on internal/outbox,
// inside one transaction (#0126). It stamps confirm_sent_at = now in the
// same UPDATE that checks the once-per-hour cooldown (PRD §6.3), so the
// check and the claim can never be split by a race the way a separate
// read-then-write would be — the property #0026's review (finding 3)
// required of the pre-#0126 ClaimConfirmationSend, preserved here — and
// then the enqueue happens in the SAME transaction, so this claim can no
// longer be stamped for a send that then fails to be queued (the failure
// mode ReleaseConfirmationClaim used to exist to compensate for; the queue
// now retries on its own, so that compensating action has no job — see
// #0126's plan §4).
//
// Returns claimed=true (and both the stamp and the enqueue commit) only if
// status is currently 'pending' AND confirm_sent_at is currently NULL or
// older than now-cooldown; otherwise nothing is changed and claimed=false —
// the cooldown is genuinely active, a concurrent request already won the
// claim a moment ago, or (see below) the row is no longer pending. The
// caller cannot tell those cases apart from this return value alone, and
// does not need to: either way, this request must not send.
//
// # #0341 — the status guard, and its twin AdminResendConfirmation
//
// The WHERE clause below carries `AND status = 'pending'`. Before #0341 this
// method trusted the caller's status read entirely: every live caller
// (subscribe.go's sendConfirmation) does read status='pending' moments
// earlier, but nothing stopped an SES complaint landing in the gap between
// that read and this claim's commit — a race #0341's review found this
// method had no defense against at all, unlike its twin below.
// AdminResendConfirmation guards the identical predicate
// (`sub.Status != StatusPending` -> ErrNotPending) in Go, against a row it
// locks with `SELECT ... FOR UPDATE` first, because that method also needs
// the locked read for its cooldown/suppression checks. This method has no
// such prior SELECT — its claim already IS an atomic conditional UPDATE
// (the cooldown check lives in the same WHERE clause), so adding `status =
// 'pending'` to that same WHERE clause gets the identical atomicity
// property without a row lock or a second query: RowsAffected()==1 remains
// the single source of truth for "this request won the claim, on a row
// that was genuinely pending at the moment the UPDATE committed."
//
// Both methods now express the same predicate — status = 'pending', the
// form #0341 preferred over a bare `<> 'complained'` because this method
// (like AdminResendConfirmation) is only ever legitimately called on a
// pending row, so requiring pending is strictly stronger and leaves no
// implicit invariant for a reader to take on faith. The two layers differ
// (SQL WHERE-clause claim here, Go check under FOR UPDATE there) because
// the two methods' surrounding transactions differ, not because the safety
// property differs — the same deliberate-divergence shape the package doc
// comment above already documents for statusLockedFromNonAdmin.
//
// # Every claim path toward a live confirm link (#0341 criterion 5)
//
// Exactly three methods in this package can stamp confirm_sent_at and
// enqueue outbox.KindConfirmation, and no other path does (the outbox
// worker sends whatever a claim already enqueued; it never re-derives or
// re-checks subscriber status — see internal/mailing.OutboxWorker):
//
//  1. Create — claims unconditionally, but only against the row it just
//     INSERTed as status='pending' in the SAME transaction; no other writer
//     has that row's id yet, so there is no window for it to already be
//     complained.
//  2. ClaimAndEnqueueConfirmation (this method) — an EXISTING row, guarded
//     as described above.
//  3. AdminResendConfirmation — an EXISTING row, guarded as described above.
//
// confirmToken/manageToken/ttl are the subscriber's OWN current values
// (i.e. sub.ConfirmToken/sub.ManageToken and the nominal TTL constant the
// caller renders with) — this method does not re-read them, so the caller
// must pass a Subscriber it just obtained from Create/RestartSignup/
// FindByEmail. (#0314, unimplemented as of this writing, plans to change
// this method to re-read and, when the existing token has expired, mint a
// fresh one under its own FOR UPDATE — a separate concern from this status
// guard. Whoever implements #0314 should fold this WHERE-clause predicate
// into that rewrite rather than drop it.)
func (s *Store) ClaimAndEnqueueConfirmation(ctx context.Context, sub Subscriber, now time.Time, cooldown time.Duration, ttl time.Duration) (claimed bool, err error) {
	if sub.ConfirmToken == nil {
		return false, fmt.Errorf("subscribers: subscriber %d has no confirm token", sub.ID)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("subscribers: beginning claim-confirmation tx for %d: %w", sub.ID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cutoff := now.Add(-cooldown)
	tag, err := tx.Exec(ctx,
		`UPDATE subscribers
		    SET confirm_sent_at = $2, updated_at = $2
		  WHERE id = $1 AND status = $4
		    AND (confirm_sent_at IS NULL OR confirm_sent_at < $3)`,
		sub.ID, now, cutoff, StatusPending,
	)
	if err != nil {
		return false, fmt.Errorf("subscribers: claiming confirmation send for %d: %w", sub.ID, err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil // cooldown active, status no longer pending, or a concurrent request already claimed this send
	}

	if _, err := s.outbox.EnqueueTx(ctx, tx, outbox.Item{
		Kind:         outbox.KindConfirmation,
		Recipient:    sub.Email,
		SubscriberID: &sub.ID,
		Payload: confirmationPayload{
			ConfirmToken: *sub.ConfirmToken,
			ManageToken:  sub.ManageToken,
			TTLSeconds:   int64(ttl.Seconds()),
		},
	}); err != nil {
		return false, fmt.Errorf("subscribers: enqueueing confirmation for %d: %w", sub.ID, err)
	}

	// #0126 phase-3 review, defect 3 — see postEnqueueCommitHook's doc
	// comment: nil in production, a test-only seam right after the
	// enqueue and before commit.
	if postEnqueueCommitHook != nil {
		if err := postEnqueueCommitHook(); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("subscribers: committing claim-confirmation tx for %d: %w", sub.ID, err)
	}
	return true, nil
}

// ClaimAndEnqueueAlreadySubscribed is ClaimAndEnqueueConfirmation's
// counterpart for the "you're already subscribed" email (PRD §6.3's active
// branch). #0026's review (finding 1) measured 20 sequential submits of one
// active subscriber's address producing 20 emails to that person — this
// path had no cooldown of any kind, unlike the confirmation email — which
// is both an unauthenticated mail-amplification vector and half of a
// two-probe enumeration oracle (see internal/handlers/subscribe.go's
// package doc comment). Same atomic claim-in-the-WHERE-clause-plus-enqueue
// shape, same reasoning, a separate column (already_subscribed_sent_at)
// because the two emails have independent cooldowns.
func (s *Store) ClaimAndEnqueueAlreadySubscribed(ctx context.Context, sub Subscriber, now time.Time, cooldown time.Duration) (claimed bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("subscribers: beginning claim-already-subscribed tx for %d: %w", sub.ID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cutoff := now.Add(-cooldown)
	tag, err := tx.Exec(ctx,
		`UPDATE subscribers
		    SET already_subscribed_sent_at = $2, updated_at = $2
		  WHERE id = $1 AND (already_subscribed_sent_at IS NULL OR already_subscribed_sent_at < $3)`,
		sub.ID, now, cutoff,
	)
	if err != nil {
		return false, fmt.Errorf("subscribers: claiming already-subscribed send for %d: %w", sub.ID, err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}

	if _, err := s.outbox.EnqueueTx(ctx, tx, outbox.Item{
		Kind:         outbox.KindAlreadySubscribed,
		Recipient:    sub.Email,
		SubscriberID: &sub.ID,
		Payload:      alreadySubscribedPayload{ManageToken: sub.ManageToken},
	}); err != nil {
		return false, fmt.Errorf("subscribers: enqueueing already-subscribed for %d: %w", sub.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("subscribers: committing claim-already-subscribed tx for %d: %w", sub.ID, err)
	}
	return true, nil
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
//
// #0127: since #0127, Confirm ALSO enqueues the welcome email
// (outbox.KindWelcome) inside this SAME transaction, immediately after
// recording the confirmed event — mirroring #0126's "committed signup can
// never have an unsent confirmation" property one step later in the flow:
// a committed confirmation can never have an unsent welcome. Two guards run
// first, inside the transaction, so neither race window is observable:
//
//   - The token is single-use (cleared to NULL by the UPDATE above), so a
//     second Confirm call with the same, now-stale token returns
//     ErrTokenInvalid before ever reaching here — "a second confirmation of
//     an already-confirmed address does not re-send" (#0127 criterion) is
//     true by construction, not by a separate check.
//   - A suppressed address never gets a welcome "even if a confirmation
//     somehow lands" (#0127 criterion): this method checks suppressions
//     directly (same package, same tx) rather than importing
//     SuppressionStore, so the check commits atomically with the
//     confirmation instead of racing a separate round trip.
//
// welcome_sent is deliberately NOT written here — see
// internal/mailing.OutboxWorker.sendOne's comment: it is written when the
// message actually LEAVES the queue (MarkSent), the same precedent
// confirmation_sent already established (#0126's plan §6), so the event
// log reflects delivery, not intent.
//
// A subscriber who unsubscribes and later resubscribes (RestartSignup) gets
// a fresh confirm_token and, on that later Confirm, a SECOND welcome email.
// This is a deliberate reading, not an oversight: the acceptance criterion
// is about TOKEN REPLAY on an already-confirmed address, and someone
// genuinely rejoining the list after leaving it is arguably owed a welcome
// again — flagged here for the reviewer since #0127 does not say either way.
//
// # #0129 — this is ALSO where an import invitation is accepted
//
// PRD §6.10.1's acceptance flow is deliberately "the SAME confirm link the
// public double opt-in flow uses" — internal/subscribers.ImportStore.Commit's
// invite branch mints its confirm_token through the identical column this
// method already reads, so no second confirmation route exists. isInvite
// (below) is derived from the row's OWN state at the moment it is locked —
// import_id set, consent_basis still NULL — rather than any flag the caller
// passes, so a website-originated confirm and an invite-originated confirm
// are told apart by what the database already knows, not by trusting the
// HTTP caller's intent:
//
//   - consent_basis is stamped ConsentBasisDoubleOptIn on EVERY successful
//     confirm whose consent_basis is currently NULL — not only invited
//     rows. A website signup has never had a consent_basis at all before
//     this change; giving it one here is consistent with PRD §6.10.1's
//     "From that point the subscriber is indistinguishable from a website
//     signup, because they are one" — both readings converge on the SAME
//     value once either confirms, which is the whole point.
//   - the welcome email is skipped for an invite acceptance — "No welcome
//     email follows an accepted invitation — the invitation was the
//     introduction" (#0129's acceptance criteria) — a website-originated
//     confirm is unaffected and still gets one.
//   - ActionInviteAccepted is recorded (in addition to ActionConfirmed,
//     which every confirm already writes), and the owning
//     subscriber_imports row's confirmed_count is incremented in this SAME
//     transaction — #0129's "Confirming... increments
//     subscriber_imports.confirmed_count" criterion.
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

	// #0129: an import-invited row that has not yet been accepted is
	// exactly import_id set + consent_basis still NULL — see this method's
	// own doc comment for why this is derived from the row rather than
	// trusted from the caller.
	isInviteAccept := sub.ImportID != nil && sub.ConsentBasis == nil

	row = tx.QueryRow(ctx,
		`UPDATE subscribers
		    SET status = $2, confirmed_at = $3, confirm_token = NULL,
		        confirm_expires_at = NULL, updated_at = $3,
		        consent_basis = COALESCE(consent_basis, $4)
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		sub.ID, StatusActive, now, ConsentBasisDoubleOptIn,
	)
	updated, err := scanSubscriber(row)
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: activating subscriber %d: %w", sub.ID, err)
	}

	if err := RecordEventTx(ctx, tx, Event{
		SubscriberID: &updated.ID,
		Email:        updated.Email,
		Action:       ActionConfirmed,
	}); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: recording confirmed event for %d: %w", updated.ID, err)
	}

	if isInviteAccept {
		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &updated.ID,
			Email:        updated.Email,
			Action:       ActionInviteAccepted,
			ImportID:     updated.ImportID,
		}); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: recording invite_accepted event for %d: %w", updated.ID, err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE subscriber_imports SET confirmed_count = confirmed_count + 1 WHERE id = $1`,
			*updated.ImportID,
		); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: incrementing confirmed_count for import %d: %w", *updated.ImportID, err)
		}
	}

	// #0127: suppressed addresses never receive the welcome email "even if
	// a confirmation somehow lands" (acceptance criterion) — checked here,
	// inside this same transaction, rather than via SuppressionStore (a
	// separate type in this package — see suppressions.go's package doc
	// comment on why — but the query itself is trivial to inline, and
	// inlining it means this check commits atomically with the
	// confirmation instead of racing a second round trip against the
	// pool). A suppressed subscriber still confirms normally; only the
	// welcome enqueue is skipped. #0129: an accepted invitation ALSO never
	// gets a welcome — "the invitation was the introduction" — regardless
	// of suppression.
	var suppressed bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM suppressions WHERE email = $1)`, updated.Email,
	).Scan(&suppressed); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: checking suppression for %d: %w", updated.ID, err)
	}

	if !suppressed && !isInviteAccept {
		interestNames, err := selectedInterestNamesTx(ctx, tx, updated.ID)
		if err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: loading interests for welcome mail, subscriber %d: %w", updated.ID, err)
		}
		if _, err := s.outbox.EnqueueTx(ctx, tx, outbox.Item{
			Kind:         outbox.KindWelcome,
			Recipient:    updated.Email,
			SubscriberID: &updated.ID,
			Payload: welcomePayload{
				ManageToken:   updated.ManageToken,
				InterestNames: interestNames,
			},
		}); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: enqueueing welcome for %d: %w", updated.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: committing confirm tx: %w", err)
	}
	return updated, nil
}

// selectedInterestNamesTx returns subscriberID's currently-selected
// interest NAMES (not ids), ordered by interests.sort_order then name — a
// raw join against the interests table this package does not otherwise
// touch (see InterestIDs above for the ids-only equivalent), used only to
// populate the welcome email's payload at confirm time (#0127). Querying by
// name here, rather than importing internal/interests, keeps this package a
// leaf (see this file's own package doc comment: "internal/subscribers
// imports nothing internal at all" other than internal/outbox).
func selectedInterestNamesTx(ctx context.Context, q outbox.Querier, subscriberID int64) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT i.name
		   FROM subscriber_interests si
		   JOIN interests i ON i.id = si.interest_id
		  WHERE si.subscriber_id = $1
		  ORDER BY i.sort_order, i.name`,
		subscriberID,
	)
	if err != nil {
		return nil, fmt.Errorf("subscribers: querying selected interest names for %d: %w", subscriberID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("subscribers: scanning interest name: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating selected interest names for %d: %w", subscriberID, err)
	}
	return out, nil
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
//
// # #0129 — this is ALSO the invitation decline path
//
// PRD §6.10.1 requires an import invitation to "carry a one-click decline
// that suppresses the address outright" — a materially stronger action than
// an ordinary unsubscribe, which deliberately does NOT suppress (see this
// method's package-level "suppression list: deliberately untouched"
// precedent in internal/handlers/unsubscribe.go's own doc comment: an
// unsubscribed address must still be able to resubscribe through ordinary
// double opt-in). Rather than build a second, parallel decline endpoint
// duplicating this method's RFC 8058 replay/complained-no-op/audit
// subtleties, invite mode's decline reuses this SAME method — every one of
// its three existing callers (one-click, preferences, admin) already goes
// through it — and this method detects, from the row's own state, whether
// it is unsubscribing a still-pending, never-confirmed import invitation:
// import_id set AND consent_basis still NULL AND status currently pending.
// That state is reachable ONLY via ImportStore.Commit's invite branch — a
// website signup never has import_id set, and a CONFIRMED invitee has
// consent_basis=ConsentBasisDoubleOptIn — so this can never fire for an
// ordinary pending website signup or an active subscriber of any
// provenance. This depends on RestartSignup clearing import_id when a
// previously-invited (and since revoked or declined) row is resurrected by
// a genuine website signup — see that method's own doc comment for the
// misfire this closes; without it, that exact resignup sequence reached
// this branch and suppressed an address that had done nothing but sign up
// and change its mind.
//
// When it fires, in the SAME transaction as the status change:
//
//   - confirm_token/confirm_expires_at are cleared, so a still-live
//     invitation link this person just declined cannot later reactivate the
//     row via Confirm.
//   - a suppressions row is added (SuppressionReasonManual) via this
//     package's own addSuppression — the same function Add/AddTx call —
//     which also writes the ActionSuppressed event for free.
//
// Every OTHER Unsubscribe caller and outcome (active→unsubscribed,
// complained no-op, an already-unsubscribed repeat call) is unchanged.
func (s *Store) Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (Subscriber, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: beginning unsubscribe tx for %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read the pre-update row inside this same transaction (FOR UPDATE, so
	// it can't change under us before the UPDATE below runs) — the full row,
	// not just status, since #0129 also needs import_id/consent_basis to
	// detect an invitation decline (see this method's own doc comment).
	// beforeStatus alone still decides whether to write an unsubscribed
	// event — #0126: writing one on every call, including a repeat call on
	// an already-unsubscribed row, would put a false "unsubscribed" entry
	// in the address's history every time a stale footer link is clicked
	// again.
	beforeRow := tx.QueryRow(ctx, `SELECT `+subscriberColumns+` FROM subscribers WHERE id = $1 FOR UPDATE`, id)
	before, err := scanSubscriber(beforeRow)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscriber{}, ErrNotFound
		}
		return Subscriber{}, fmt.Errorf("subscribers: locking subscriber %d for unsubscribe: %w", id, err)
	}
	beforeStatus := before.Status
	isInviteDecline := beforeStatus == StatusPending && before.ImportID != nil && before.ConsentBasis == nil

	row := tx.QueryRow(ctx,
		`UPDATE subscribers
		    SET status             = CASE WHEN status = $5 THEN status             ELSE $2 END,
		        unsubscribed_at    = CASE WHEN status = $5 THEN unsubscribed_at    ELSE $3 END,
		        unsubscribe_source = CASE WHEN status = $5 THEN unsubscribe_source ELSE $4 END,
		        confirm_token      = CASE WHEN status = $5 THEN confirm_token      WHEN $6 THEN NULL ELSE confirm_token      END,
		        confirm_expires_at = CASE WHEN status = $5 THEN confirm_expires_at WHEN $6 THEN NULL ELSE confirm_expires_at END,
		        updated_at         = CASE WHEN status = $5 THEN updated_at         ELSE $3 END
		  WHERE id = $1
		 RETURNING `+subscriberColumns,
		id, StatusUnsubscribed, now, source, statusLockedFromNonAdmin, isInviteDecline,
	)
	sub, err := scanSubscriber(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Subscriber{}, ErrNotFound
	case err != nil:
		return Subscriber{}, fmt.Errorf("subscribers: unsubscribing %d: %w", id, err)
	}

	if sub.Status == StatusUnsubscribed && beforeStatus != StatusUnsubscribed {
		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &sub.ID,
			Email:        sub.Email,
			Action:       ActionUnsubscribed,
			Detail:       map[string]any{"source": source},
		}); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: recording unsubscribed event for %d: %w", sub.ID, err)
		}

		if isInviteDecline {
			if _, err := addSuppression(ctx, tx, NewSuppression{
				Email:  sub.Email,
				Reason: SuppressionReasonManual,
				Note:   "declined an import invitation before confirming",
			}, now); err != nil {
				return Subscriber{}, fmt.Errorf("subscribers: suppressing declined invitee %d: %w", sub.ID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: committing unsubscribe tx for %d: %w", id, err)
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
// # #0324 — also clears unsubscribed_at and unsubscribe_source
//
// Before this, a departure survived a restart when the corresponding arrival
// did not: confirmed_at is cleared above (the row's earlier confirmation no
// longer applies — it is being treated as a brand new, unconfirmed signup),
// but unsubscribed_at was left exactly as Unsubscribe had stamped it. On an
// ordinary website signup — no import involved — Growth30Days'
// unsubscribed_30d counts `unsubscribed_at >= since` unconditionally, so a
// person who confirms, unsubscribes, and restarts inside the same 30-day
// window read as net -1 (the confirmation's own +1 having just vanished from
// confirmed_30d, while the departure's -1 stayed): a row with no live
// arrival counted in the window was still generating a departure in it, the
// same "arrival without a matching event" class of bug #0311 fixed for
// imports, just reached a different way. Clearing unsubscribed_at and
// unsubscribe_source here — the same treatment confirmed_at already gets —
// keeps the pair symmetric: a restarted row contributes to neither
// confirmed_30d nor unsubscribed_30d until whatever happens to it next
// (a fresh confirmation, a fresh unsubscribe) writes a new event of its own.
// The prior unsubscribe is not lost from the audit trail — subscriber_events
// already carries the ActionUnsubscribed row with its own timestamp, which
// is where "when did this address previously leave" is answered from, the
// same reasoning #0129's import_id-clearing paragraph below already uses for
// why clearing a column here does not erase history.
//
// # #0129 — also clears import_id, so a revoked-then-resignedup row cannot
// be mistaken for a live invitation
//
// Confirm, Unsubscribe (decline path), AdminResendConfirmation, and
// ExpirePendingSweep all infer "this row is an unaccepted import
// invitation" from import_id being set with consent_basis still NULL —
// deliberately, so acceptance is decided by what the database already
// knows rather than a flag the caller passes (see Confirm's own doc
// comment). Before this method cleared import_id, that inference misfired:
// invite → revoke (Revoke leaves import_id set and consent_basis NULL on a
// still-pending row) → the SAME address signs up on the website weeks
// later routes through this method (existingSignup's StatusUnsubscribed
// branch), which used to leave import_id in place. The row was then a
// genuine website signup in progress that every one of those four call
// sites still read as an unaccepted invitation — worst of all, Unsubscribe
// would SUPPRESS it outright on the person's very next unsubscribe, with no
// self-service recovery (found in this issue's review). Once this method
// no longer treats consent as import-derived — the person's own action
// (visiting the site and submitting the form) is what starts this new
// signup, not the old import — clearing import_id here is the same
// reasoning Revoke's own doc comment already uses for a confirmed invitee:
// "its consent no longer derives from the import." The audit trail is not
// lost: subscriber_events already carries ImportID on the earlier
// imported/invite_sent rows, which is where "why did we ever mail this
// address" is answered from.
//
// # #0336 — also clears consent_basis, so a restarted row carries no stale
// standing-consent claim forward
//
// consent_basis is otherwise append-only: the only writer anywhere in this
// tree is Confirm's `consent_basis = COALESCE(consent_basis, $4)` (that
// method's own doc comment; re-confirmed by grep for every SQL statement
// that assigns the column — ImportStore.Commit's two INSERT branches are the
// only other writer, and INSERT is not a later mutation). Before this
// change, this method left consent_basis exactly as it was, which was fine
// for import_id above (a live invitation must not be mistaken for one after
// a restart) but wrong for consent_basis itself: an accepted invitation's
// consent_basis is ConsentBasisDoubleOptIn and a prior_consent import's is
// ConsentBasisImportedPriorConsent, and both values are stamped once and
// never retracted — so a row that unsubscribes and restarts kept carrying
// its OLD standing consent forward into a brand-new, unconfirmed signup that
// has not yet earned any. Growth30Days' imported_30d (that method's own doc
// comment) reads consent_basis to decide whether an import batch's own
// attestation is what currently justifies the address being on the list; a
// restarted row's earlier attestation no longer does, since the person is
// mid-way through proving fresh consent via a brand-new confirm_token this
// same UPDATE just minted. Clearing it here is the identical reasoning
// already applied to confirmed_at and import_id in the two sections above:
// the restart discards standing evidence from before, not just the pending
// state built on it. If the restarted signup is later confirmed, Confirm's
// COALESCE stamps ConsentBasisDoubleOptIn fresh, exactly as it would for any
// other pending row with no consent_basis yet — nothing distinguishes a
// restarted row's eventual confirmation from an ordinary one's. The prior
// value is not lost from the audit trail: subscriber_events already carries
// the ActionImported/ActionInviteAccepted/ActionConfirmed rows that recorded
// it in the first place.
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: beginning restart-signup tx for %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read the pre-update status inside this same transaction, FOR UPDATE,
	// purely to decide which events to write below (#0126) — resubscribed
	// only applies when the prior status was genuinely unsubscribed, not
	// bounced (existingSignup routes both statuses through this method;
	// see subscribe.go's own comment on why bounced is routed here too).
	var beforeStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM subscribers WHERE id = $1 FOR UPDATE`, id).Scan(&beforeStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscriber{}, ErrNotFound
		}
		return Subscriber{}, fmt.Errorf("subscribers: locking subscriber %d for restart-signup: %w", id, err)
	}

	row := tx.QueryRow(ctx,
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
		        updated_at         = CASE WHEN status = $11 THEN updated_at         ELSE $10   END,
		        import_id          = CASE WHEN status = $11 THEN import_id         ELSE NULL   END,
		        unsubscribed_at    = CASE WHEN status = $11 THEN unsubscribed_at    ELSE NULL  END,
		        unsubscribe_source = CASE WHEN status = $11 THEN unsubscribe_source ELSE NULL  END,
		        consent_basis      = CASE WHEN status = $11 THEN consent_basis      ELSE NULL  END
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

	if sub.Status == StatusPending && beforeStatus != statusLockedFromNonAdmin {
		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &sub.ID,
			Email:        sub.Email,
			Action:       ActionSignupRequested,
			Detail:       map[string]any{"kind": "restarted"},
		}); err != nil {
			return Subscriber{}, fmt.Errorf("subscribers: recording signup_requested for restart %d: %w", sub.ID, err)
		}
		if beforeStatus == StatusUnsubscribed {
			if err := RecordEventTx(ctx, tx, Event{
				SubscriberID: &sub.ID,
				Email:        sub.Email,
				Action:       ActionResubscribed,
			}); err != nil {
				return Subscriber{}, fmt.Errorf("subscribers: recording resubscribed event for %d: %w", sub.ID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Subscriber{}, fmt.Errorf("subscribers: committing restart-signup tx for %d: %w", id, err)
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

// IncrementSoftBounceStreakTx is #0124's transactional counterpart to
// MarkBouncedTx: it increments subscribers.soft_bounce_streak by one and
// stamps last_bounce_at, run against q so ses_notifications.go's Transient/
// Undetermined bounce branch can commit it atomically with its own
// email_events insert and suppressions.AddTx call, exactly like
// MarkBouncedTx's own atomicity requirement (see that method's doc
// comment). Returns the streak value AFTER this bounce — the caller
// compares it against soft_bounce_threshold_count in the same call, with no
// second query — not the whole row, since nothing else the caller needs
// changed.
//
// Deliberately does NOT consult statusLockedFromNonAdmin: streak tracking
// is orthogonal to the status guard that protects `complained` (CLAUDE.md
// §9). A complained subscriber accumulating a streak is harmless — they are
// already suppressed and mail has already stopped regardless of what this
// number does — and refusing to increment it here would just make the
// history less complete for no protective effect. What matters is that
// nothing in this package ever uses a streak, high or low, to move a
// subscriber OUT of complained; nothing does (see RecordDeliveryTx and
// ResetSoftBounceStreakByEmail below, neither of which touches status).
func (s *Store) IncrementSoftBounceStreakTx(ctx context.Context, q querier, id int64, now time.Time) (streak int, err error) {
	err = q.QueryRow(ctx,
		`UPDATE subscribers
		    SET soft_bounce_streak = soft_bounce_streak + 1,
		        last_bounce_at     = $2,
		        updated_at         = $2
		  WHERE id = $1
		 RETURNING soft_bounce_streak`,
		id, now,
	).Scan(&streak)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, ErrNotFound
	case err != nil:
		return 0, fmt.Errorf("subscribers: incrementing soft bounce streak for %d: %w", id, err)
	}
	return streak, nil
}

// RecordDeliveryTx is #0124's Delivery-event write: a SES Delivery report
// for this address zeroes soft_bounce_streak and stamps last_delivery_at —
// the "reset on success" half of the consecutive-streak rule (PRD §6.9),
// which is the entire reason a streak, unlike the rolling window it
// replaces, has a notion of "the address recovered". Run against q for the
// same atomicity reason as IncrementSoftBounceStreakTx. Also does not
// consult statusLockedFromNonAdmin — a Delivery event never changes status,
// only the streak, so there is nothing here that could move a subscriber
// out of complained.
func (s *Store) RecordDeliveryTx(ctx context.Context, q querier, id int64, now time.Time) error {
	tag, err := q.Exec(ctx,
		`UPDATE subscribers
		    SET soft_bounce_streak = 0,
		        last_delivery_at   = $2,
		        updated_at         = $2
		  WHERE id = $1`,
		id, now,
	)
	if err != nil {
		return fmt.Errorf("subscribers: recording delivery for %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetSoftBounceStreakByEmail zeroes soft_bounce_streak through the shared
// pool for the subscriber matching email. Pool-only (not a q querier
// parameter) so it can sit behind a genuine narrow interface in
// internal/handlers (CLAUDE.md §1) — its one external caller, POST
// /admin/deliverability/{email}/reset-streak (an explicit, audited admin
// action). suppressions.go's SuppressionStore.Remove has the identical
// #0124 criterion ("removing a suppression resets the streak to 0") but
// calls the unexported resetSoftBounceStreakByEmail directly instead of
// this method, since it is a pool call in the same package with no need to
// cross a package boundary.
//
// A no-op (0 rows affected, no error) when no subscribers row exists for
// email — a suppression can predate or outlive any subscribers row for the
// same address (suppressions.go's own doc comment) — so a caller that needs
// to know whether a row existed checks GetByID/FindByEmail itself rather
// than infer it from this method's return.
func (s *Store) ResetSoftBounceStreakByEmail(ctx context.Context, email string, now time.Time) error {
	return resetSoftBounceStreakByEmail(ctx, s.pool, email, now)
}

// resetSoftBounceStreakByEmail is the free-function implementation shared
// by Store.ResetSoftBounceStreakByEmail and SuppressionStore.Remove
// (suppressions.go) — the latter has no *Store handle (it is deliberately
// its own type; see that file's package doc comment) but needs the
// identical reset, so this is a package-level function rather than
// forcing SuppressionStore to hold a *Store just to reach one method.
func resetSoftBounceStreakByEmail(ctx context.Context, q querier, email string, now time.Time) error {
	_, err := q.Exec(ctx,
		`UPDATE subscribers SET soft_bounce_streak = 0, updated_at = $2 WHERE email = lower(trim($1))`,
		email, now,
	)
	if err != nil {
		return fmt.Errorf("subscribers: resetting soft bounce streak for %q: %w", email, err)
	}
	return nil
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
	subscribers.unsubscribe_source, subscribers.source, subscribers.source_detail,
	subscribers.consent_basis, subscribers.import_id, subscribers.invited_at,
	subscribers.soft_bounce_streak, subscribers.last_bounce_at,
	subscribers.last_delivery_at, subscribers.created_at, subscribers.updated_at,
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

// Growth30Days returns three counts over the trailing window starting at
// since (the caller passes now.Add(-30*24*time.Hour); kept as a parameter
// rather than computed here so the result is deterministic in tests, the
// same convention every other now-sensitive method on this store follows,
// e.g. Confirm's own now parameter): how many subscribers locally confirmed
// (completed double opt-in) since that time, how many entered the list via
// an import since that time, and how many unsubscribed since that time.
// #0061's admin overview dashboard sums the first two and subtracts the
// third for a net growth figure; all three are returned rather than
// pre-combined so the dashboard can show each direction rather than only
// the net.
//
// "Confirmed" here means confirmed_at is set — NOT "became active": since
// #0292, a prior_consent CSV import (internal/subscribers.ImportStore.Commit)
// lands a subscriber `active` with confirmed_at left NULL, because PRD
// §6.10 is explicit that such a row "did not confirm here". Counting those
// rows would inflate this figure with addresses that never went through
// this list's own confirmation flow — an import of 500 addresses must not
// read as 500 confirmations on the dashboard. This method's name and first
// return value are unchanged (the dashboard still shows "confirmed_30d");
// what changed is which subscribers rows can now be active without ever
// tripping it.
//
// "Imported" (#0305, revised by #0311, revised again by #0324) counts
// `source = 'import' AND consent_basis IS NOT NULL AND confirmed_at IS NULL
// AND created_at >= since` — an EVENT count (did an import batch place this
// address on the list, with its own standing consent, within the window),
// not a snapshot of current status. #0305's original form additionally
// required `status = 'active'`, which made it a CURRENT-STATE count: a
// subscriber who left inside the window stopped being 'active' and so
// retracted their own imported_30d contribution AT THE SAME TIME
// unsubscribed_30d counted their departure, netting -1 for a subscriber who
// merely joined and left (#0311) — and ImportStore.Revoke makes that
// reachable in bulk, since a revoked batch's rows all move to 'unsubscribed'
// together. #0311 fixed that by dropping the status clause, but in doing so
// widened the predicate to `source = 'import' AND confirmed_at IS NULL`,
// which ALSO matches an invite-mode row the instant ImportStore.Commit sends
// the invitation (#0129: inserted `status=pending, source='import',
// confirmed_at NULL`) — reversing #0305's own recorded decision that "a
// pending invite-mode row is not growth... an invitation is not an accepted
// subscription." A 500-address invite import read as +500 before a single
// acceptance, and stayed there permanently once every invitation expired
// (ExpirePendingSweep leaves the row pending forever; nothing ever clears
// it out of the window).
//
// #0324 restored #0305's decision with `consent_basis IS NOT NULL`, and
// #0336 narrowed it further, to `consent_basis = $3`
// (ConsentBasisImportedPriorConsent — ImportStore.Commit, imports.go): this
// bucket means "an import batch's OWN attestation is what currently
// justifies this row being on the list", not merely "consent_basis happens
// to be set to something". #0324's `IS NOT NULL` form conflated those two —
// harmlessly for a never-restarted row (an accepted invite always has
// confirmed_at set the instant Confirm stamps ConsentBasisDoubleOptIn, so
// the `confirmed_at IS NULL` guard already excluded it) but not for one that
// later restarts: RestartSignup clears confirmed_at (a fresh, unconfirmed
// signup) yet, before #0336, left consent_basis exactly as Confirm had
// stamped it — ConsentBasisDoubleOptIn, not ConsentBasisImportedPriorConsent
// — so the row satisfied `IS NOT NULL AND confirmed_at IS NULL` again and
// re-entered imported_30d on a stale, since-superseded consent record, while
// sitting merely `pending` with no import ever having attested to it. #0336
// closes this at BOTH ends: this predicate now demands the exact
// prior_consent value, and RestartSignup (see that method's own #0336 doc
// comment) now clears consent_basis on every restart, so a restarted row
// never carries forward a consent_basis value from before — belt and
// braces, either change alone is sufficient for the invite-accept case, but
// only RestartSignup's clearing fixes the prior_consent-import case below.
//
// A prior_consent import that later restarts (unsubscribe → RestartSignup,
// no re-import involved) is the second #0336 case: its consent_basis was
// exactly ConsentBasisImportedPriorConsent from INSERT, so narrowing this
// predicate's equality target does NOT exclude it on its own — an exact
// match is still an exact match after a restart that leaves the column
// untouched. It is RestartSignup's own clearing of consent_basis, not this
// predicate, that withdraws that row's contribution: once consent_basis is
// NULL, `= ConsentBasisImportedPriorConsent` is false regardless of what the
// value used to be, and the row correctly stops counting as imported until
// (if ever) it earns fresh consent through its own new confirm_token.
//
// This still does not reopen #0311's stock-vs-event bug for the ordinary
// (non-restarted) case: consent_basis is append-only EXCEPT at RestartSignup
// (Confirm's COALESCE is its only other writer, and INSERT — verified by
// grep across every .go file and every migration for an assignment to this
// column — is not a later mutation), so a prior_consent row that leaves
// without ever restarting stays counted here, the same append-only property
// #0311 relied on for `source`.
//
// `source = 'import'` alone is still not enough: source is never rewritten
// after INSERT (see the Subscriber.Source field's own #0317 doc comment), so
// an accepted import INVITE keeps source='import' forever after Confirm sets
// its confirmed_at — without the `confirmed_at IS NULL` guard it would count
// in BOTH imported_30d and confirmed_30d the moment it is accepted inside
// the same window it was sent, double-counting one join as two.
// `confirmed_at IS NULL` is exactly the test that already defines
// "confirmed" for the first return value, so an import row that later
// completes a genuine local confirmation (#0129's invite-accept path) falls
// out of "imported" the instant it falls into "confirmed" — the two buckets
// stay mutually exclusive on the way in, verified by #0305's review and
// unchanged by #0311 or #0324: a subscriber is in exactly one of
// {confirmed, imported, neither (e.g. still-pending website signup or a
// still-unaccepted invitation)} at any moment, never both.
//
// "Unsubscribed" (#0061, revised by #0324) counts `unsubscribed_at >= $1
// AND NOT (source = 'import' AND consent_basis IS NULL)` — the design
// question #0324 was filed to settle: what a revoked or expired unaccepted
// invitation should count as in unsubscribed_30d, now that imported_30d no
// longer counts it as an arrival.
//
// # #0336 — this guard and imported_30d's are deliberately NOT the same test
//
// #0324's doc comment (through #0336) claimed this guard's negation and
// imported_30d's `consent_basis IS NOT NULL` were "the SAME test... applied
// on both sides of net_30d". #0336 narrowed the arrival side to an exact
// match against ConsentBasisImportedPriorConsent (see that paragraph above),
// which breaks the claim as stated: `= ConsentBasisImportedPriorConsent` and
// `IS NULL` are not negations of each other once a THIRD value
// (ConsentBasisDoubleOptIn) is in play. This guard is left as `IS NULL`
// rather than narrowed to match — deliberately, not by oversight — because
// the two sides are answering different questions. imported_30d asks "did an
// import batch's OWN attestation place this row on the list" (a narrow,
// single-source question); this guard asks "did this row EVER have ANY
// standing consent behind it, from any source" (a broad question, because a
// departure's exclusion here must cover every arrival this method could ever
// have counted, not just imported_30d's). consent_basis is NULL for exactly
// one class of row: an import-sourced address nobody has ever consented for
// — never accepted (still pending) or restarted since (#0336's RestartSignup
// clearing, above) — and that is precisely the class this guard must
// exclude, regardless of which arrival bucket an accepted version of the
// same row would have landed in.
//
// Concretely, the two predicates necessarily agree for a NULL or
// ConsentBasisImportedPriorConsent value (the only two an import row can
// hold without ever having been confirmed) and necessarily differ for
// ConsentBasisDoubleOptIn (an accepted invitation): imported_30d excludes it
// unconditionally now (it is not this import's own attestation, it is the
// person's own later confirmation — confirmed_30d's job), while this guard
// must still COUNT its departure, because that confirmation DID count as
// confirmed_30d growth and the departure has to balance it. Narrowing this
// guard to mirror imported_30d's equality check would silently stop counting
// an accepted invitee's later unsubscribe — a real regression, not a
// symmetry improvement — which is exactly why #0336 left it as `IS NULL`
// rather than "fixing" it to match.
//
// Walked through by row: ExpirePendingSweep never touches unsubscribed_at at
// all (the row stays pending), so an expired unaccepted invitation needs no
// guard from this predicate. ImportStore.Revoke and Store.Unsubscribe's
// invite-decline branch both DO stamp unsubscribed_at on a row whose
// consent_basis is still NULL — this guard excludes those, matching that
// imported_30d never counted them as an arrival either. A prior_consent
// import that is later revoked or unsubscribed WITHOUT ever restarting keeps
// consent_basis = ConsentBasisImportedPriorConsent (append-only outside
// RestartSignup) and is unaffected by this guard, continuing to count as a
// departure that matches its own arrival — the ordinary join-then-leave
// symmetry #0311 established. An accepted invitee who later unsubscribes
// (again, no restart) has consent_basis = ConsentBasisDoubleOptIn — not
// NULL — so the departure counts, mirroring that the acceptance itself
// counted as confirmed_30d growth, exactly as before #0336. And a row of
// EITHER provenance that unsubscribes, then restarts (RestartSignup's #0336
// clearing sets consent_basis back to NULL and unsubscribed_at back to NULL
// in the SAME UPDATE — see that method's own doc comment) contributes to
// neither imported_30d, confirmed_30d, nor unsubscribed_30d until whatever
// happens to the restarted row next writes a new event of its own — which is
// the fix for #0336's rows O and P.
//
// The dashboard's net_30d is confirmed + imported - unsubscribed — the
// import branch stopped being counted as a confirmation (#0292) but must
// still show up as growth (#0305) once actually accepted or unconditionally
// consented to (#0324), stays counted as growth even after the address
// leaves for a case that WAS counted as growth (#0311), and now neither an
// unaccepted invitation nor its later revocation or expiry moves net_30d in
// either direction (#0324) — a 500-address invite batch that nobody accepts
// reads as flat, not +500 and not -500.
//
// #0311 also checked confirmed_30d (`confirmed_at >= $1`) for the same class
// of defect: it does not filter on current status at all, so there is no
// stock-vs-event mismatch in that predicate as written here. confirmed_at is
// not literally append-only forever — RestartSignup clears it back to NULL
// when a row that unsubscribed (or bounced) restarts as a fresh
// self-initiated signup — but Unsubscribe itself never touches confirmed_at,
// so the ordinary join-then-leave cycle #0311 was about (no restart
// involved) nets 0 exactly as its own table shows. #0311 left the
// restart-after-departure sequence as a separate, pre-existing asymmetry
// outside its scope; #0324 closes it in RestartSignup itself (see that
// method's own doc comment) by clearing unsubscribed_at/unsubscribe_source
// there too, rather than by complicating this predicate — a restarted row
// stops being counted as EITHER an arrival or a departure, exactly like a
// row that never had either event happen to it, so the persistent -1 an
// abandoned restart used to leave behind is gone at the source.
//
// Excludes synthetic=true rows unconditionally, same as StatusCounts above
// — #0061's amendment (issue notes, from #0046's second phase-3 review)
// requires this of any new aggregate that counts subscribers rows directly,
// for the same reason: a per-admin campaign test-send fixture is not a real
// signup or a real departure.
func (s *Store) Growth30Days(ctx context.Context, since time.Time) (confirmed, imported, unsubscribed int64, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT
		    count(*) FILTER (WHERE confirmed_at >= $1),
		    count(*) FILTER (WHERE source = $2 AND consent_basis = $3 AND confirmed_at IS NULL AND created_at >= $1),
		    count(*) FILTER (WHERE unsubscribed_at >= $1 AND NOT (source = $2 AND consent_basis IS NULL))
		 FROM subscribers WHERE synthetic = false`,
		since, SubscriberSourceImport, ConsentBasisImportedPriorConsent,
	).Scan(&confirmed, &imported, &unsubscribed)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("subscribers: computing 30-day growth: %w", err)
	}
	return confirmed, imported, unsubscribed, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), e.g. a duplicate email or token collision.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

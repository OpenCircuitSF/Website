// Package outbox is the durable transport for transactional mail (PRD
// §6.11; #0126) — the outbound_queue table (migrations/000021), a row per
// message claimed by a worker with exponential backoff and an orphan sweep,
// exactly the shape internal/mailing's email_sends already established for
// campaign mail.
//
// # Why this is a leaf package
//
// `go list -deps` says internal/mailing -> internal/subscribers, and
// internal/subscribers imports nothing internal at all. So subscribers can
// never import mailing. If the queue lived in internal/mailing,
// subscribers.Store.Create/Confirm/etc could not enqueue inside their own
// transaction — which is the single property #0126 exists to establish (a
// committed signup can never have an unsent confirmation). internal/outbox
// therefore imports nothing internal — specifically not internal/mailing,
// since subscribers -> outbox -> mailing -> subscribers would be a cycle.
// It owns the table, the Kind constants, and the claim/backoff/sweep SQL; it
// does not know what an email looks like or how to send one — that is
// internal/mailing's OutboxWorker (outbox_worker.go), which imports this
// package and internal/mailing.Mailer/the Build* template family.
//
// # payload is template inputs, not rendered MIME
//
// Storing inputs (a confirm token, a manage token, a recovery token) rather
// than rendered HTML/text means a template fix applies to mail already
// queued. That does mean a 'sent' row archives a live secret in a second
// table for as long as the row exists — MarkSent blanks payload to
// '{}'::jsonb in the same UPDATE that stamps sent_at, so the token's
// lifetime in this table equals the row's queued lifetime, not its whole
// retention. attempts/ses_message_id/sent_at — everything with forensic
// value about how the send happened — survive that blank. error does NOT
// (#0272): MarkSent clears it in the same UPDATE, because error describes a
// live fault and a 'sent' row has none — a prior failed attempt's error
// text sitting next to status='sent' reads as a failure to anyone scanning
// the table, which is worse than no history. A 'queued' row awaiting retry
// and a terminal 'abandoned' row both keep error; only 'sent' clears it. An
// 'abandoned' row also keeps its payload: that row IS the diagnostic, and
// the token is dead anyway (the send that would have used it never
// happened and the caller's own cooldown/claim logic governs whether a
// fresh one gets issued).
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind is the closed-ish set of outbound_queue.kind values this project's
// Go code knows how to render. The column itself carries no CHECK
// constraint (PRD §6.2 gives it as a SQL comment, not an enum), so adding a
// kind here never touches a migration or either PRD parity guard — see
// #0126's plan §9 item 5.
type Kind string

const (
	KindConfirmation      Kind = "confirmation"
	KindAlreadySubscribed Kind = "already_subscribed"
	KindWelcome           Kind = "welcome"     // producer lands in #0127
	KindGoodbye           Kind = "goodbye"     // no producer yet — reserved
	KindAdminAlert        Kind = "admin_alert" // producer lands in #0124
	KindRegistration      Kind = "registration"
	KindRecovery          Kind = "recovery"
	KindSessionsRevoked   Kind = "sessions_revoked" // existed pre-#0126 (#0076); moved onto the queue here
	KindImportInvite      Kind = "import_invite"    // producer lands in #0129

	// KindSubscribeIntake (#0254) is NOT an email — it is not one of the
	// kinds internal/mailing.OutboxWorker's render switch knows how to
	// build a message for. It is this queue's mechanism reused (per
	// CLAUDE.md's "reuse #0126's queue rather than build a second one") for
	// a different durability gap: #0126 made "committed signup ⇒ queued
	// confirmation" durable (Store.Create/ClaimAndEnqueueConfirmation
	// commit the outbound_queue INSERT in the same transaction as the
	// state change), but internal/handlers.SubscribeHandler's *inbound*
	// mutation — the honeypot/timing verdict, the two fixed reads, and
	// which of Create/RestartSignup/SetInterests/ClaimAndEnqueue* to call —
	// still lived only in an in-memory channel (mutateQueue) that a
	// process kill or a full queue silently dropped, after the client had
	// already received its 202. A row of this kind is the durable record
	// of "a 202 was returned for this address" — written synchronously,
	// before the response — and internal/handlers.SubscribeHandler's own
	// recovery poller (not internal/mailing.OutboxWorker — see
	// ClaimDue's kinds filter below) claims and reprocesses any row still
	// 'queued' once the fast in-memory path's own generous grace window has
	// passed, exactly reusing ClaimDue/OrphanSweep/MarkSent and nothing
	// else. "sent" for a row of this kind means "the signup attempt was
	// durably processed", not that any mail went out from THIS row —
	// whatever mail results (confirmation, already_subscribed, or none)
	// is a SEPARATE outbound_queue row, enqueued the ordinary way inside
	// internal/subscribers.Store's own transactions.
	KindSubscribeIntake Kind = "subscribe_intake"
)

// Status values, matching outbound_queue_status_check.
const (
	StatusQueued    = "queued"
	StatusSending   = "sending"
	StatusSent      = "sent"
	StatusFailed    = "failed" // reserved; unused by this issue — see package doc comment
	StatusAbandoned = "abandoned"
)

// DefaultMaxRetries is the fallback used when the settings.queue_max_retries
// row is missing or fails to parse as a positive integer — the same
// degrade-gracefully convention internal/handlers/soft_bounce.go already
// establishes for its own settings-backed constant.
const DefaultMaxRetries = 8

// Querier is the subset of pgx Store uses. *pgxpool.Pool and pgx.Tx both
// satisfy it, matching internal/audit's querier/WriteTx and
// internal/subscribers' querier/AddTx idiom for the identical reason:
// EnqueueTx must be callable against a caller-owned transaction so the
// insert commits or rolls back atomically with the state change that
// caused it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Item is one message to enqueue.
type Item struct {
	Kind         Kind
	Recipient    string
	SubscriberID *int64
	// Payload is marshalled to JSONB — template inputs, not rendered MIME.
	// A nil Payload stores '{}'::jsonb (outbound_queue.payload is NOT
	// NULL), never SQL NULL.
	Payload any
	// Delay pushes the row's initial next_attempt_at into the future by
	// this much (zero, the default, means "due immediately" — identical to
	// every caller before #0254). #0254's SubscribeHandler is the one user:
	// its fast, in-memory processing path normally finishes a
	// KindSubscribeIntake row in well under a second, so giving it a
	// generous head start before SubscribeHandler's own recovery poller
	// becomes eligible to reclaim the SAME row avoids routine double
	// processing under normal operation — reprocessing is safe either way
	// (idempotent, see subscribe_intake.go's package doc comment), this
	// purely reduces needless duplicate work.
	Delay time.Duration
}

// Row is one outbound_queue row, as returned by ClaimDue.
type Row struct {
	ID            int64
	Kind          Kind
	Recipient     string
	SubscriberID  *int64
	Payload       json.RawMessage
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	SESMessageID  *string
	Error         *string
	ClaimedAt     *time.Time
	SentAt        *time.Time
	CreatedAt     time.Time
}

// Counts is the queue-depth summary #0061's admin overview reads.
type Counts struct {
	Queued              int64
	Sending             int64
	Sent                int64
	Abandoned           int64
	OldestQueuedAgeSecs int64 // 0 when Queued == 0
}

// Store is the data-access layer over outbound_queue.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// backoffSchedule is PRD §6.11's six-step schedule. Attempts beyond len(this)
// clamp to the last step (24h) — see Backoff's doc comment for why.
var backoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// Backoff returns the delay before the next attempt, given attempts (the
// 1-based attempt number just completed — ClaimDue increments attempts as
// part of the claim, so inside a send it is already the current attempt's
// number). PRD §6.11 pairs a six-step schedule with queue.max_retries
// defaulting to 8 and says nothing about attempts 7 and 8; this clamps to
// the schedule's last step (24h) rather than leaving them undefined, since
// stopping retrying at six would contradict the stated default of 8. Do not
// "fix" this to extend the schedule — it is deliberate, see #0126's plan §2.
func Backoff(attempts int) time.Duration {
	idx := attempts - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	return backoffSchedule[idx]
}

// EnqueueTx inserts it through q — the pool or a caller-owned transaction —
// and returns the new row's id. This is the only enqueue any state-changing
// caller may use when the enqueue must commit atomically with the write
// that caused it (see the package doc comment); Enqueue below is for
// callers with no transaction of their own (e.g. login.go's
// sessions_revoked notice, which is already non-fatal and has no state
// change to be atomic with).
func (s *Store) EnqueueTx(ctx context.Context, q Querier, it Item) (int64, error) {
	payload := it.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("outbox: marshalling payload for kind %q: %w", it.Kind, err)
	}

	// it.Delay's zero value yields now() + 0 seconds, identical to the
	// column's own DEFAULT now() every caller before #0254 relied on
	// implicitly.
	var id int64
	err = q.QueryRow(ctx,
		`INSERT INTO outbound_queue (kind, recipient, subscriber_id, payload, next_attempt_at)
		 VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5))
		 RETURNING id`,
		string(it.Kind), it.Recipient, it.SubscriberID, body, it.Delay.Seconds(),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("outbox: enqueueing kind %q for %q: %w", it.Kind, it.Recipient, err)
	}
	return id, nil
}

// Enqueue is EnqueueTx against the shared pool, for callers with no
// transaction of their own.
func (s *Store) Enqueue(ctx context.Context, it Item) (int64, error) {
	return s.EnqueueTx(ctx, s.pool, it)
}

const rowColumns = `id, kind, recipient, subscriber_id, payload, status, attempts,
	next_attempt_at, ses_message_id, error, claimed_at, sent_at, created_at`

func scanRow(row pgx.Row) (Row, error) {
	var r Row
	var kind string
	err := row.Scan(
		&r.ID, &kind, &r.Recipient, &r.SubscriberID, &r.Payload, &r.Status, &r.Attempts,
		&r.NextAttemptAt, &r.SESMessageID, &r.Error, &r.ClaimedAt, &r.SentAt, &r.CreatedAt,
	)
	if err != nil {
		return Row{}, err
	}
	r.Kind = Kind(kind)
	return r, nil
}

// ClaimDue atomically claims up to limit rows that are queued and due
// (next_attempt_at <= now()), stamping status='sending', attempts+1, and
// claimed_at=now() in one UPDATE — the same shape as
// internal/mailing.SendStore.ClaimRow, so incrementing attempts as part of
// the claim (not after a failed send) satisfies the same
// increment-before-the-send-attempt requirement that store carries.
// FOR UPDATE SKIP LOCKED on the inner SELECT: unlike email_sends, whose
// worker is single and serial per campaign, this queue may in principle be
// polled by more than one worker goroutine, so this is free insurance
// rather than a strict requirement with today's single OutboxWorker.
//
// kinds optionally restricts the claim to those Kind values — pass none to
// claim across every kind (every caller before #0254 did, and still may).
// #0254 added this because outbound_queue now has TWO independent
// consumers, each polling with its own ClaimDue call:
// internal/mailing.OutboxWorker (every email Kind) and
// internal/handlers.SubscribeHandler's recovery poller (KindSubscribeIntake
// only, which is not an email — see that Kind's doc comment). Without a
// filter, either consumer could claim a row it has no idea how to process:
// OutboxWorker's render switch has no case for subscribe_intake, and the
// intake poller doesn't render or send mail at all.
func (s *Store) ClaimDue(ctx context.Context, limit int, kinds ...Kind) ([]Row, error) {
	kindStrs := make([]string, len(kinds))
	for i, k := range kinds {
		kindStrs[i] = string(k)
	}
	rows, err := s.pool.Query(ctx,
		`UPDATE outbound_queue
		    SET status = $2, attempts = attempts + 1, claimed_at = now()
		  WHERE id IN (
		          SELECT id FROM outbound_queue
		           WHERE status = $3 AND next_attempt_at <= now()
		             AND (cardinality($4::text[]) = 0 OR kind = ANY($4::text[]))
		           ORDER BY next_attempt_at, id
		           LIMIT $1
		           FOR UPDATE SKIP LOCKED)
		RETURNING `+rowColumns,
		limit, StatusSending, StatusQueued, kindStrs,
	)
	if err != nil {
		return nil, fmt.Errorf("outbox: claiming due rows: %w", err)
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("outbox: scanning claimed row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: iterating claimed rows: %w", err)
	}
	return out, nil
}

// MarkSent transitions a claimed row to 'sent', stamping ses_message_id and
// sent_at, blanking payload to '{}'::jsonb — see the package doc comment's
// "payload is template inputs" section for why — and clearing error in the
// SAME UPDATE (#0272). error exists to diagnose a live fault:
// MarkRetryOrAbandon deliberately keeps it for a 'queued' row awaiting retry
// and for a terminal 'abandoned' row (that row IS the diagnostic), but a row
// that went on to succeed has no live fault left to describe, and a
// populated error next to status='sent' reads as a failure to anyone
// scanning the table by eye — which is exactly what happened with
// production ids 1 and 2 (see #0272). This does not change when a row is
// clearable, only what a 'sent' row carries once it gets there.
//
// # #0283 — the WHERE predicate admits 'sending' OR 'queued', not just 'sending'
//
// There are TWO call sites, and the safety argument below rests on both, not
// just the first one found by grep:
//
//   - internal/mailing.OutboxWorker.sendOne calls MarkSent synchronously,
//     immediately after a real SES call that just succeeded for THIS row.
//   - internal/handlers.SubscribeHandler.processIntakeRow
//     (subscribe_intake.go) calls MarkSent for KindSubscribeIntake rows,
//     which are NOT email and involve no SES call at all — see that Kind's
//     doc comment above. There, "sent" means "dispatchMutation ran to
//     completion for this row", not "SES accepted a message".
//
// What both call sites actually guarantee, and what this predicate depends
// on, is narrower than "a real SES call": every invocation follows an
// operation that genuinely completed for this row — a successful
// mailer.Send for the email kinds, or a finished dispatchMutation call for
// KindSubscribeIntake — never a stale or speculative one. The only question
// is what status the row is allowed to be in by the time this UPDATE runs,
// since something else may have moved it between the ClaimDue that handed
// the caller this row and the MarkSent that records the outcome.
//
// #0283 was filed because the strict original guard (status='sending' only)
// meant a lost claim produced a genuine duplicate send: the message had
// already gone to SES, but because the row was no longer 'sending', MarkSent
// affected zero rows, the row stayed 'queued', and a later pass claimed and
// sent it again — a duplicate confirmation, registration, or recovery link,
// which is exactly what this subsystem exists to prevent. The proven cause
// (an unscoped OrphanSweep stealing a claim across outbound_queue's two
// independent consumers) is now structurally impossible: #0254 scoped both
// ClaimDue and OrphanSweep by kind, and its reviewer verified neither
// consumer's sweep can touch the other's rows in either direction. So this
// predicate is defence in depth against a race that is no longer reachable
// by the route that motivated it — not a live bug — which is why the fix
// widens the guard just enough to close the failure mode rather than going
// fully unconditional.
//
// Every source status this predicate can see, and what admitting or
// excluding it means:
//
//   - 'sending' — the expected case: this row is still claimed by the
//     goroutine that is calling MarkSent. Admitted, unchanged from before.
//   - 'queued' — the claim was released or stolen back to 'queued' while the
//     operation this call just completed was still in flight (Release, or a
//     same-kind OrphanSweep racing a still-live claim — the residual,
//     intra-kind version of #0254's now-fixed cross-kind race). Newly
//     admitted by #0283. What recording 'sent' here means differs by kind.
//     For the email kinds: SES already has the message; recording 'sent'
//     here is what stops a second pass from claiming and sending it again.
//     For KindSubscribeIntake: dispatchMutation already ran to completion;
//     recording 'sent' here stops a second pass from reprocessing the same
//     row. Reprocessing it would in fact be SAFE to repeat, not just
//     wasteful — verified against the actual code, not assumed:
//     processIntakeRow re-reads IsSuppressed/FindByEmail fresh rather than
//     trusting anything captured earlier (subscribe_intake.go's own doc
//     comment and its "Why reprocessing is safe even if it races the fast
//     path" section), and dispatchMutation is idempotent by construction —
//     newSignup's ErrEmailExists branch (subscribe.go) makes a raced Create
//     a safe no-op, and ClaimAndEnqueueConfirmation/
//     ClaimAndEnqueueAlreadySubscribed (internal/subscribers.Store) are
//     atomic, cooldown-gated claims where a second attempt to send the same
//     mail is already a required no-op for ordinary concurrent requests
//     (two tabs, a double-submit), not a new property added for this
//     predicate. Admitting 'queued' here still avoids the redundant work of
//     re-running a dispatch whose outcome is already durable.
//   - 'sent' — a second MarkSent call for the same id (e.g. a duplicate
//     invocation racing itself) would otherwise re-stamp sent_at and
//     ses_message_id. Excluded, so this stays a no-op — the same idempotency
//     TestOutbox_MarkSent_ScrubsPayload already asserts. For
//     KindSubscribeIntake, 'sent' here means "durably processed", not
//     "delivered by SES" (see that Kind's doc comment), but the exclusion is
//     identical either way: a second call for an already-processed row must
//     stay a no-op regardless of what 'sent' means for that kind.
//   - 'abandoned' — this row already reached the terminal state that
//     MarkRetryOrAbandon uses to mean "exhausted its retries", keeping its
//     error and payload as the diagnostic (see MarkRetryOrAbandon's doc
//     comment). Silently flipping that to 'sent' would erase a decision an
//     operator can currently see, in exchange for closing a race that, per
//     the analysis above, the architecture no longer produces. #0283
//     criterion 2 is explicit that resurrecting an abandoned row must be a
//     deliberate, tested choice, not a side effect of a broader predicate —
//     so it stays excluded here. Revisit only alongside a concrete scenario
//     that reaches it, not speculatively.
//   - 'failed' — reserved, unused by any writer (see the Status block above).
//     Excluded for the same reason as 'abandoned': nothing should silently
//     overwrite a status this method's own callers never produce.
//
// Returns done=false if the row was not in 'sending' or 'queued' (already
// 'sent', already 'abandoned', or an id that does not exist) — the caller
// must not treat that as an error; OutboxWorker.sendOne logs it as a Warn
// (#0254), which stays exactly as it was regardless of this decision
// (#0283 criterion 5) — it is the signal that something took a claim, and
// that remains worth knowing even now that the outcome is handled.
func (s *Store) MarkSent(ctx context.Context, id int64, messageID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue
		    SET status = $2, ses_message_id = $3, sent_at = now(), payload = '{}'::jsonb, error = NULL
		  WHERE id = $1 AND status IN ($4, $5)`,
		id, StatusSent, nullIfEmpty(messageID), StatusSending, StatusQueued,
	)
	if err != nil {
		return false, fmt.Errorf("outbox: marking %d sent: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkDone transitions a row STRAIGHT to 'sent' from either 'queued' or
// 'sending' — unlike MarkSent, which requires the row to already be
// 'sending' (i.e. already claimed via ClaimDue). #0254 added this for
// KindSubscribeIntake's fast, in-memory processing path
// (internal/handlers.SubscribeHandler.markIntakeDone), which finishes a row
// without ever calling ClaimDue on it: under normal operation nothing else
// has touched the row, so it is still exactly 'queued'. Accepting 'sending'
// too covers the case where SubscribeHandler's own recovery poller
// (subscribe_intake.go) raced in and claimed the row first — both paths
// converging on the same row is expected and safe (see that file's package
// doc comment), and whichever call loses the race simply affects zero
// rows, which this method's callers must not treat as an error, exactly as
// MarkSent's own doc comment already establishes for its equivalent case.
// No messageID: KindSubscribeIntake rows are not email, so ses_message_id
// stays NULL — see that Kind's doc comment for why "sent" here means "the
// signup attempt was durably processed", not that mail went out.
//
// error is cleared in the same UPDATE (#0288 — the identical fix #0272
// applied to MarkSent), mirrored here because MarkDone has the exact same
// shape and the exact same reachable convergence: the recovery poller
// claims a row, fails it via MarkRetryOrAbandon (which writes error and
// puts the row back to 'queued'), and the request's own fast path then
// MarkDones that same row. Without this, the row lands 'sent' carrying the
// error from an attempt that was subsequently superseded — indistinguishable
// from a live fault to anyone scanning the table, exactly the failure mode
// #0272 closed for MarkSent.
func (s *Store) MarkDone(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue
		    SET status = $2, sent_at = now(), payload = '{}'::jsonb, claimed_at = NULL, error = NULL
		  WHERE id = $1 AND status IN ($3, $4)`,
		id, StatusSent, StatusQueued, StatusSending,
	)
	if err != nil {
		return false, fmt.Errorf("outbox: marking %d done: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkRetryOrAbandon transitions a claimed row back to 'queued' with the
// next backoff step, or to the terminal 'abandoned' state (retaining the
// last error) once attempts has reached maxRetries. attempts is the row's
// own post-claim attempt count (ClaimDue's Row.Attempts) — the caller
// passes it rather than this method re-deriving it from the schedule
// itself, so Backoff (above) stays the single source of truth for the
// schedule instead of duplicating it in SQL. Returns done=false on the same
// "row was not in 'sending'" condition MarkSent documents.
//
// next_attempt_at is only recomputed on the 'queued' branch — on the
// 'abandoned' branch (#0126 phase-3 review, minor finding) a next attempt
// time is meaningless (nothing retries a terminal row), so that branch
// leaves the column as-is rather than computing and discarding a backoff
// step nobody will read.
//
// #0283 re-check: this method's own WHERE clause (status = 'sending') is
// unchanged by that issue's widening of MarkSent's predicate. The two
// cannot race each other productively — MarkRetryOrAbandon only ever fires
// from OutboxWorker.finishFailed, on the SAME row a SES send just failed
// for, so a given claim's lifecycle calls at most one of MarkSent or
// MarkRetryOrAbandon, never both. The only cross-claim interaction is a
// stolen-claim race (see MarkSent's #0283 comment): if a second actor
// reclaims a released row and calls MarkRetryOrAbandon on it (status
// 'sending' from that actor's own claim), and the first actor's delayed
// MarkSent then arrives, MarkSent's widened predicate no longer sees
// 'sending' or 'queued' once MarkRetryOrAbandon has moved the row to
// 'abandoned' — so it correctly affects zero rows and logs the Warn rather
// than resurrecting an abandoned row, exactly as MarkSent's own comment
// requires. If instead MarkRetryOrAbandon's 'queued' branch ran (attempts
// still under maxRetries), a subsequent MarkSent still finds the row
// 'queued' and correctly records the send that in fact reached SES,
// clearing the error that retry attempt had written — the intended
// behaviour, not a side effect.
func (s *Store) MarkRetryOrAbandon(ctx context.Context, id int64, attempts int, errMsg string, maxRetries int) (bool, error) {
	delaySecs := Backoff(attempts).Seconds()
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue
		    SET status = CASE WHEN attempts >= $3 THEN $5 ELSE $6 END,
		        error = $2,
		        next_attempt_at = CASE WHEN attempts >= $3 THEN next_attempt_at ELSE now() + make_interval(secs => $4) END,
		        claimed_at = NULL
		  WHERE id = $1 AND status = $7`,
		id, nullIfEmpty(errMsg), maxRetries, delaySecs, StatusAbandoned, StatusQueued, StatusSending,
	)
	if err != nil {
		return false, fmt.Errorf("outbox: retry/abandon for %d: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Release reverts a claimed-but-unsent row back to 'queued' immediately
// (next_attempt_at = now()), clearing claimed_at — used by
// OutboxWorker.Stop at shutdown so a row that was claimed but not yet sent
// is retried on restart rather than waiting out orphanStaleAfter.
func (s *Store) Release(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue
		    SET status = $2, claimed_at = NULL, next_attempt_at = now()
		  WHERE id = $1 AND status = $3`,
		id, StatusQueued, StatusSending,
	)
	if err != nil {
		return false, fmt.Errorf("outbox: releasing %d: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// OrphanSweep resets rows stuck in 'sending' back to 'queued', but ONLY
// rows whose claim is stale: claimed_at IS NULL or older than staleAfter.
// Mirrors internal/mailing.SendStore.OrphanSweep exactly, including the
// staleness gate #0122 added there after an unconditional sweep was found
// to un-claim a row a live worker was still mid-send on and mail it twice —
// see that method's doc comment for the full incident. Copying the fix, not
// the defect that preceded it, is #0126's plan §2's explicit instruction.
//
// kinds optionally restricts the sweep to those Kind values, mirroring
// ClaimDue's own kinds filter above — pass none to sweep across every kind
// (the prior, unfiltered behavior). #0254's review bounce is why this
// parameter exists: that commit added a kinds filter to ClaimDue but not to
// OrphanSweep, and gave OrphanSweep a second caller —
// internal/handlers.SubscribeHandler's recovery poller, sweeping every 5s
// with a 20s staleness window sized for ITS OWN fast path
// (intakeOrphanStaleAfter). internal/mailing.OutboxWorker legitimately
// holds a mail row 'sending' for up to ~35s (sendMessageTimeout +
// writeStatusTimeout, outboxOrphanStaleAfter=70s), so the unfiltered 20s
// sweep could — and, proven in that review, did — release a live mail
// claim mid-send. The eventual MarkSent then affects zero rows (the row is
// no longer 'sending'; sendOne discards that bool), the row stays
// 'queued', and the next mailing pass claims and sends it AGAIN: a
// duplicate confirmation, registration magic link, or recovery link.
// Exactly like ClaimDue, neither of outbound_queue's two independent
// consumers may sweep a row it does not own.
func (s *Store) OrphanSweep(ctx context.Context, staleAfter time.Duration, kinds ...Kind) (int64, error) {
	kindStrs := make([]string, len(kinds))
	for i, k := range kinds {
		kindStrs[i] = string(k)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue SET status = $1, claimed_at = NULL
		  WHERE status = $2
		    AND (claimed_at IS NULL OR claimed_at < now() - $3::interval)
		    AND (cardinality($4::text[]) = 0 OR kind = ANY($4::text[]))`,
		StatusQueued, StatusSending, staleAfter, kindStrs,
	)
	if err != nil {
		return 0, fmt.Errorf("outbox: sweeping orphaned rows: %w", err)
	}
	return tag.RowsAffected(), nil
}

// LatestByRecipients returns, for every recipient in recipients that has at
// least one outbound_queue row of kind, that row's MOST RECENT state (by
// created_at) — one query, not one per address, for #0128's pending-
// subscriber screen ("the outbound queue state for each pending address").
// A recipient with no matching row is simply absent from the returned map;
// the caller renders that as "never queued" rather than treating it as an
// error. An empty recipients slice returns an empty map without querying.
func (s *Store) LatestByRecipients(ctx context.Context, kind Kind, recipients []string) (map[string]Row, error) {
	out := make(map[string]Row)
	if len(recipients) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (recipient) `+rowColumns+`
		   FROM outbound_queue
		  WHERE kind = $1 AND recipient = ANY($2)
		  ORDER BY recipient, created_at DESC, id DESC`,
		string(kind), recipients,
	)
	if err != nil {
		return nil, fmt.Errorf("outbox: loading latest rows by recipient: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("outbox: scanning latest-by-recipient row: %w", err)
		}
		out[r.Recipient] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: iterating latest-by-recipient rows: %w", err)
	}
	return out, nil
}

// AbandonedCountByKind returns how many outbound_queue rows of kind have
// reached the terminal 'abandoned' state — #0128's "count of confirmations
// abandoned in the queue" on the admin overview, scoped to one kind rather
// than Counts' account-wide total across every kind.
func (s *Store) AbandonedCountByKind(ctx context.Context, kind Kind) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM outbound_queue WHERE kind = $1 AND status = $2`,
		string(kind), StatusAbandoned,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("outbox: counting abandoned rows for kind %q: %w", kind, err)
	}
	return n, nil
}

// Counts returns the queue-depth summary for #0061's admin overview.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	var oldestSecs *float64
	err := s.pool.QueryRow(ctx,
		`SELECT
		    count(*) FILTER (WHERE status = $1),
		    count(*) FILTER (WHERE status = $2),
		    count(*) FILTER (WHERE status = $3),
		    count(*) FILTER (WHERE status = $4),
		    extract(epoch FROM now() - min(created_at) FILTER (WHERE status = $1))
		 FROM outbound_queue`,
		StatusQueued, StatusSending, StatusSent, StatusAbandoned,
	).Scan(&c.Queued, &c.Sending, &c.Sent, &c.Abandoned, &oldestSecs)
	if err != nil {
		return Counts{}, fmt.Errorf("outbox: counting: %w", err)
	}
	if oldestSecs != nil {
		c.OldestQueuedAgeSecs = int64(*oldestSecs)
	}
	return c, nil
}

// nullIfEmpty maps an empty string to SQL NULL, matching the same helper in
// internal/subscribers and internal/audit.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

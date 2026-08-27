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
// retention. attempts/error/ses_message_id/sent_at — everything with
// forensic value — survive that blank. An 'abandoned' row keeps its
// payload: that row IS the diagnostic, and the token is dead anyway (the
// send that would have used it never happened and the caller's own
// cooldown/claim logic governs whether a fresh one gets issued).
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

// MarkSent transitions a claimed (status='sending') row to 'sent', stamping
// ses_message_id and sent_at and blanking payload to '{}'::jsonb — see the
// package doc comment's "payload is template inputs" section for why.
// Returns done=false if the row was not in 'sending' (already handled by a
// concurrent claim, or an id that does not exist) — the caller must not
// treat that as an error.
func (s *Store) MarkSent(ctx context.Context, id int64, messageID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue
		    SET status = $2, ses_message_id = $3, sent_at = now(), payload = '{}'::jsonb
		  WHERE id = $1 AND status = $4`,
		id, StatusSent, nullIfEmpty(messageID), StatusSending,
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
func (s *Store) MarkDone(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue
		    SET status = $2, sent_at = now(), payload = '{}'::jsonb, claimed_at = NULL
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
func (s *Store) OrphanSweep(ctx context.Context, staleAfter time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outbound_queue SET status = $1, claimed_at = NULL
		  WHERE status = $2
		    AND (claimed_at IS NULL OR claimed_at < now() - $3::interval)`,
		StatusQueued, StatusSending, staleAfter,
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

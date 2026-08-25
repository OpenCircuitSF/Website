// worker_store.go is the send worker's own data-access layer (#0045, PRD
// §6.6): the campaign claim/resume/demote transitions, the orphan sweep, the
// per-row batch claim, the sent/failed row transitions, the completion
// check, and GatherPreflight — the one place that reads every fact
// preflight.go's Preflight function needs, so the worker and the advisory
// HTTP call site (internal/handlers' campaignPreflightChecker adapter)
// gather identically.
//
// Kept separate from CampaignStore (campaigns.go, #0041's operator-facing
// CRUD): these are the worker's own transitions
// (scheduled->sending->sent/failed), never driven by the admin API, exactly
// as campaigns.go's own package doc comment says CampaignStore deliberately
// does NOT own them.
//
// # test_sent_at freshness (carried in from #0041's review)
//
// The no_test_send gate checks TestSentAt != nil, not TestSentAt >=
// updated_at: any status transition bumps updated_at, so a freshness
// comparison would invalidate a legitimate test send the moment the
// campaign was scheduled, deadlocking the operator. If a test send must be
// invalidated by a content edit, CampaignStore.Update is where that belongs
// (clearing test_sent_at to NULL when subject/body_md change) — see that
// method's own doc comment, which #0045 extends per the carried-in
// obligation from #0041's implementation.
package mailing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsReader is the narrow settings dependency the worker and
// GatherPreflight need — GetSetting(ctx, key) (value string, err error).
// *auth.Store satisfies it (the same shape internal/handlers'
// softBounceSettingsReader already establishes for the identical purpose).
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// settingPhysicalAddress and settingDefaultFromName are the settings keys
// this issue reads. settingMaxSendRate is read once per batch by the
// worker's rate limiter (worker.go).
//
// settingSendHealthMinSample/BouncePct/ComplaintPct (#0124, PRD §6.9) are
// the circuit breaker's own settings — seeded by migrations/000015
// alongside physical_address/max_send_rate's precedent. Exported (not just
// package-private consts) so internal/handlers/settings.go's
// validSettingValue can validate PATCH /admin/settings against the exact
// same keys the worker reads, with no risk of the two drifting — the same
// reason internal/handlers/soft_bounce.go's settingSoftBounceThresholdCount
// is shared between ses_notifications.go and admin_subscribers.go.
const (
	settingPhysicalAddress = "physical_address"
	settingDefaultFromName = "default_from_name"
	settingMaxSendRate     = "max_send_rate"

	SettingSendHealthMinSample    = "send_health_min_sample"
	SettingSendHealthBouncePct    = "send_health_bounce_pct"
	SettingSendHealthComplaintPct = "send_health_complaint_pct"
)

// Defaults for the circuit breaker settings above, matching PRD §6.9's
// table and migrations/000015's seed exactly. Used as the in-code fallback
// when a row is missing or holds a value that fails to parse — the same
// degrade-gracefully convention as defaultSoftBounceThresholdCount
// (internal/handlers/soft_bounce.go) and settingMaxSendRate's own fallback
// to MAX_SEND_RATE.
const (
	DefaultSendHealthMinSample    = 50
	DefaultSendHealthBouncePct    = 5.0
	DefaultSendHealthComplaintPct = 0.1
)

// claimedCampaign is one email_campaigns row plus its targeting, as
// returned by ClaimStart/ClaimResume — everything drainCampaign (worker.go)
// needs to materialize and drain it without a second round trip.
type claimedCampaign struct {
	ID             int64
	Subject        string
	Preheader      *string
	BodyMD         string
	AudienceMode   string
	InterestIDs    []int64
	TestSentAt     *time.Time
	MaterializedAt *time.Time
	CreatedBy      *int64
}

// SendStore is the send worker's data-access layer over email_campaigns and
// email_sends, plus GatherPreflight's cross-package reads (settings, a dry
// render). It holds the same static campaign-mail inputs the worker itself
// does (baseURL/listDomain/replyTo) so GatherPreflight can assemble an
// identical PreflightInput at either call site.
type SendStore struct {
	pool     *pgxpool.Pool
	audience *AudienceStore
	settings SettingsReader
	render   CampaignRenderer

	baseURL, listDomain, replyTo string
}

// NewSendStore constructs a SendStore. render may be nil, defaulting to
// MarkdownCampaignRenderer{} (the production renderer) — tests inject a
// fake to exercise RenderErr without a real Markdown pass.
func NewSendStore(pool *pgxpool.Pool, audience *AudienceStore, settings SettingsReader, render CampaignRenderer, baseURL, listDomain, replyTo string) *SendStore {
	if render == nil {
		render = MarkdownCampaignRenderer{}
	}
	return &SendStore{
		pool:       pool,
		audience:   audience,
		settings:   settings,
		render:     render,
		baseURL:    baseURL,
		listDomain: listDomain,
		replyTo:    replyTo,
	}
}

const claimedCampaignColumns = `id, subject, preheader, body_md, audience_mode, test_sent_at, materialized_at, created_by`

func scanClaimedCampaign(row pgx.Row) (*claimedCampaign, error) {
	var c claimedCampaign
	if err := row.Scan(&c.ID, &c.Subject, &c.Preheader, &c.BodyMD, &c.AudienceMode, &c.TestSentAt, &c.MaterializedAt, &c.CreatedBy); err != nil {
		return nil, err
	}
	return &c, nil
}

// interestIDs returns the sorted interest ids attached to a campaign. A
// local duplicate of CampaignStore.interestIDs / AudienceStore's own
// campaignInterestIDs — both unexported on different types over the same
// one-line query, so duplicating it here follows the precedent
// audience.go's own doc comment already sets: cheaper and clearer than
// exporting a method solely for this one call site.
func (s *SendStore) interestIDs(ctx context.Context, campaignID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT interest_id FROM campaign_interests WHERE campaign_id = $1 ORDER BY interest_id`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("mailing: loading interests for campaign %d: %w", campaignID, err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("mailing: scanning campaign interest row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mailing: iterating campaign interest rows: %w", err)
	}
	return ids, nil
}

// PeekScheduledCandidate returns the id of the next campaign the start-claim
// path should evaluate — status='scheduled' and due — or ok=false if none is
// due. A plain read, not FOR UPDATE SKIP LOCKED: the gate that follows (§2 of
// this issue's plan) is itself a read, and the actual mutual exclusion is
// each of DemoteToDraft/ClaimStart's own `WHERE status='scheduled'` guard —
// two workers racing to read the same candidate can both evaluate the gate,
// but only one's subsequent UPDATE affects a row.
func (s *SendStore) PeekScheduledCandidate(ctx context.Context) (int64, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM email_campaigns
		  WHERE status = $1 AND scheduled_at <= now()
		  ORDER BY scheduled_at, id
		  LIMIT 1`, CampaignStatusScheduled,
	).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("mailing: peeking scheduled campaigns: %w", err)
	}
	return id, true, nil
}

// DemoteToDraft moves a 'scheduled' campaign back to 'draft' — the gate's
// refusal path (§2). Returns done=false when the row no longer matched (a
// concurrent worker already claimed or demoted it), in which case the
// caller must NOT write a send_refused audit row — only the worker that
// actually performed the transition does.
func (s *SendStore) DemoteToDraft(ctx context.Context, campaignID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_campaigns SET status = $2, updated_at = now() WHERE id = $1 AND status = $3`,
		campaignID, CampaignStatusDraft, CampaignStatusScheduled,
	)
	if err != nil {
		return false, fmt.Errorf("mailing: demoting campaign %d to draft: %w", campaignID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimStart performs the atomic "start claim" (#0045's plan §4): the one
// transition into 'sending' that may happen exactly once per campaign. The
// `WHERE status='scheduled'` predicate inside the subquery is the mutual
// exclusion — after the first UPDATE commits, no second claimant matches;
// FOR UPDATE SKIP LOCKED keeps two pollers from blocking on each other.
// Returns nil (not an error) when nothing matched — the caller must not
// treat that as "no campaigns due", only as "this one is gone".
func (s *SendStore) ClaimStart(ctx context.Context, campaignID int64) (*claimedCampaign, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE email_campaigns
		    SET status = $2, started_at = now(), updated_at = now()
		  WHERE id = (
		          SELECT id FROM email_campaigns
		           WHERE id = $1 AND status = $3
		           FOR UPDATE SKIP LOCKED)
		RETURNING `+claimedCampaignColumns,
		campaignID, CampaignStatusSending, CampaignStatusScheduled,
	)
	c, err := scanClaimedCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("mailing: claiming campaign %d to start sending: %w", campaignID, err)
	}
	ids, err := s.interestIDs(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	c.InterestIDs = ids
	return c, nil
}

// ClaimResume returns the campaign already 'sending' when the worker polls —
// a restart mid-send, the normal case after any deploy — or nil if none is
// in that state. No FOR UPDATE: a 'sending' campaign is legitimately drained
// by more than one concurrently-running Worker at once (that is exactly what
// makes two workers on one campaign safe — see the per-row claim in
// ClaimBatch/ClaimRow, which is the actual mutual exclusion for individual
// rows). Ordered by started_at so a worker resumes the oldest in-flight
// campaign first.
func (s *SendStore) ClaimResume(ctx context.Context) (*claimedCampaign, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+claimedCampaignColumns+`
		   FROM email_campaigns
		  WHERE status = $1
		  ORDER BY started_at, id
		  LIMIT 1`, CampaignStatusSending,
	)
	c, err := scanClaimedCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("mailing: claiming a resumed campaign: %w", err)
	}
	ids, err := s.interestIDs(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	c.InterestIDs = ids
	return c, nil
}

// OrphanSweep resets rows stuck in 'sending' for campaignID back to
// 'queued' — but ONLY rows whose claim is stale: claimed_at IS NULL (a
// 'sending' row that never went through ClaimRow) or claimed_at is older
// than staleAfter's window. Run once per claim (start or resume), before
// draining.
//
// staleAfter is a plain duration, not an absolute cutoff — the caller
// (worker.go's drainCampaign) hands over orphanStaleAfter itself and this
// statement computes `claimed_at < now() - $2::interval` entirely in
// Postgres. An earlier version of this fix computed the cutoff from the Go
// process's own clock (w.now()) and compared it against claimed_at, which
// Postgres stamps with ITS clock — correct only because both run on the
// same host today, and undocumented as a dependency. Comparing against
// Postgres's own now() on both sides of the predicate removes the
// dependency entirely rather than merely noting it. orphanStaleAfter is
// derived from the two hard timeouts (sendMessageTimeout,
// writeStatusTimeout) that already bound how long a LIVE claim can
// legitimately sit in 'sending'. Before #0122 this statement had no
// staleness check at all, so it could not tell a crashed worker's abandoned
// row from a live worker's in-flight one — a second worker's resume could
// un-claim a row the first worker was still holding and mail it twice
// (TestWorker_TwoWorkersOneCampaign_OrphanSweepDuringLiveClaim_NoDuplicate).
// Returns the number of rows reset (0 in the ordinary case where nothing
// crashed).
func (s *SendStore) OrphanSweep(ctx context.Context, campaignID int64, staleAfter time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_sends SET status = 'queued'
		  WHERE campaign_id = $1 AND status = 'sending'
		    AND (claimed_at IS NULL OR claimed_at < now() - $2::interval)`,
		campaignID, staleAfter,
	)
	if err != nil {
		return 0, fmt.Errorf("mailing: sweeping orphaned sends for campaign %d: %w", campaignID, err)
	}
	return tag.RowsAffected(), nil
}

// SetMaterializedAt stamps materialized_at on the "materialize exactly
// once" transition. Returns done=false when another worker already stamped
// it first (the race both workers can lose or win, never both win) — the
// caller only writes the campaign.send_started audit row when done is true.
func (s *SendStore) SetMaterializedAt(ctx context.Context, campaignID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_campaigns SET materialized_at = now() WHERE id = $1 AND materialized_at IS NULL`,
		campaignID,
	)
	if err != nil {
		return false, fmt.Errorf("mailing: stamping materialized_at for campaign %d: %w", campaignID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountEmailSends returns the total, sent, failed, skipped, queued, and
// sending row counts for campaignID — the authoritative post-materialization
// audience size (worker.go's anomalous-empty-audience check), the
// completion audit's metadata, and #0048's progress denominator.
func (s *SendStore) CountEmailSends(ctx context.Context, campaignID int64) (total, sent, failed, skipped, queued, sending int64, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE status = 'sent'),
		        count(*) FILTER (WHERE status = 'failed'),
		        count(*) FILTER (WHERE status = 'skipped'),
		        count(*) FILTER (WHERE status = 'queued'),
		        count(*) FILTER (WHERE status = 'sending')
		   FROM email_sends WHERE campaign_id = $1`, campaignID,
	).Scan(&total, &sent, &failed, &skipped, &queued, &sending)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, fmt.Errorf("mailing: counting sends for campaign %d: %w", campaignID, err)
	}
	return total, sent, failed, skipped, queued, sending, nil
}

// ClaimBatch runs #0044's transaction shape unchanged (BEGIN; SELECT ... FOR
// UPDATE SKIP LOCKED; RecheckEligibleTx; MarkSkippedTx; COMMIT) and returns
// only the still-eligible Recipients — the per-row atomic claim that
// actually takes each one out of 'queued' happens separately, per recipient,
// OUTSIDE this transaction (ClaimRow) — see this issue's plan §4 for why:
// holding this transaction open across the batch's SES calls would roll back
// every status update on a mid-batch crash, turning one crash into a
// batch-sized duplicate instead of a one-message window.
//
// Skipped rows are grouped by Reason and MarkSkippedTx is called once per
// group (carried in from #0044's review, 2026-08-21) — the plan's
// single-call sketch would collapse a mixed batch to one reason string and
// lose the per-row detail #0049's stats view wants.
func (s *SendStore) ClaimBatch(ctx context.Context, campaignID int64, limit int) ([]Recipient, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("mailing: beginning claim-batch tx for campaign %d: %w", campaignID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id FROM email_sends
		  WHERE campaign_id = $1 AND status = 'queued'
		  ORDER BY id LIMIT $2
		  FOR UPDATE SKIP LOCKED`, campaignID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("mailing: selecting queued batch for campaign %d: %w", campaignID, err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("mailing: scanning queued batch row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("mailing: iterating queued batch rows: %w", err)
	}
	rows.Close()

	if len(ids) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("mailing: committing empty claim-batch tx for campaign %d: %w", campaignID, err)
		}
		return nil, nil
	}

	recipients, skipped, err := s.audience.RecheckEligibleTx(ctx, tx, campaignID, ids)
	if err != nil {
		return nil, err
	}

	if len(skipped) > 0 {
		groups := make(map[string][]int64, 4)
		var order []string
		for _, sk := range skipped {
			if _, seen := groups[sk.Reason]; !seen {
				order = append(order, sk.Reason)
			}
			groups[sk.Reason] = append(groups[sk.Reason], sk.SendID)
		}
		for _, reason := range order {
			if _, err := s.audience.MarkSkippedTx(ctx, tx, campaignID, groups[reason], reason); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("mailing: committing claim-batch tx for campaign %d: %w", campaignID, err)
	}
	return recipients, nil
}

// ClaimRow performs the per-row atomic claim (§4 of this issue's plan): the
// same statement that increments attempts, satisfying #0044's "increment
// before the SES call" requirement in one round trip. It also stamps
// claimed_at = now() (#0122) — the timestamp OrphanSweep uses to tell this
// row apart from one a crashed worker abandoned. Returns claimed=false when
// the row is no longer 'queued' — a concurrent worker already has it (or it
// was skipped between ClaimBatch's recheck and this call, which cannot
// happen within one worker's own sequential loop but can across two
// workers) — the caller must send nothing in that case.
func (s *SendStore) ClaimRow(ctx context.Context, sendID int64) (attempts int, claimed bool, err error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE email_sends SET status = 'sending', attempts = attempts + 1, claimed_at = now()
		  WHERE id = $1 AND status = 'queued'
		RETURNING attempts`, sendID,
	)
	if err := row.Scan(&attempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("mailing: claiming send row %d: %w", sendID, err)
	}
	return attempts, true, nil
}

// ReleaseRow returns a claimed-but-unsent row to 'queued' without touching
// attempts further — used when a terminalCampaign-class error stops the
// drain mid-row (§8), so the row is available the moment the campaign is
// retried rather than waiting for a future orphan sweep to notice it.
func (s *SendStore) ReleaseRow(ctx context.Context, sendID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_sends SET status = 'queued' WHERE id = $1 AND status = 'sending'`, sendID,
	)
	if err != nil {
		return false, fmt.Errorf("mailing: releasing send row %d: %w", sendID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// maxSendErrorLength bounds email_sends.error — an AWS error string can
// carry a request id and a long message, and the column is for diagnosis,
// not archival (§8 of this issue's plan).
const maxSendErrorLength = 500

func truncateSendError(msg string) string {
	if len(msg) <= maxSendErrorLength {
		return msg
	}
	return msg[:maxSendErrorLength]
}

// MarkSent transitions a claimed row to 'sent' after a successful SES call.
func (s *SendStore) MarkSent(ctx context.Context, sendID int64, messageID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_sends SET status = 'sent', ses_message_id = $2, sent_at = now()
		  WHERE id = $1 AND status = 'sending'`, sendID, messageID,
	)
	if err != nil {
		return false, fmt.Errorf("mailing: marking send row %d sent: %w", sendID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkFailedRow transitions a claimed row straight to 'failed' regardless of
// attempts — for a terminalRow-class error (§8): retrying a per-recipient
// rejection three times is three more rejections.
func (s *SendStore) MarkFailedRow(ctx context.Context, sendID int64, errMsg string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_sends SET status = 'failed', error = $2 WHERE id = $1 AND status = 'sending'`,
		sendID, truncateSendError(errMsg),
	)
	if err != nil {
		return false, fmt.Errorf("mailing: marking send row %d failed: %w", sendID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkRetryOrFailed applies §8's retryable classification: back to 'queued'
// unless attempts has already reached 3 (incremented by ClaimRow before the
// SES call), in which case it becomes 'failed' — three attempts total, per
// the acceptance criterion.
func (s *SendStore) MarkRetryOrFailed(ctx context.Context, sendID int64, errMsg string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_sends
		    SET status = CASE WHEN attempts >= 3 THEN 'failed' ELSE 'queued' END,
		        error = $2
		  WHERE id = $1 AND status = 'sending'`, sendID, truncateSendError(errMsg),
	)
	if err != nil {
		return false, fmt.Errorf("mailing: retry-or-fail on send row %d: %w", sendID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// CompleteIfDone transitions campaignID to 'sent' when no queued or sending
// rows remain. A 'canceled' campaign no longer matches `status='sending'`,
// so this is a harmless no-op once #0041's Cancel has run — cancel stops
// further claims; it cannot recall anything already sent.
func (s *SendStore) CompleteIfDone(ctx context.Context, campaignID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_campaigns SET status = 'sent', completed_at = now(), updated_at = now()
		  WHERE id = $1 AND status = 'sending'
		    AND NOT EXISTS (SELECT 1 FROM email_sends
		                     WHERE campaign_id = $1 AND status IN ('queued', 'sending'))`,
		campaignID,
	)
	if err != nil {
		return false, fmt.Errorf("mailing: completing campaign %d: %w", campaignID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkFailedCampaign transitions a 'sending' campaign to 'failed' — the
// anomalous-empty-audience case, a physical_address/reply-to that went
// blank mid-resume, or a terminalCampaign-class SES error (§8). Guarded by
// `AND status='sending'` so a second worker's concurrent failure attempt (or
// a completion that raced it) is a harmless no-op rather than a double
// transition.
func (s *SendStore) MarkFailedCampaign(ctx context.Context, campaignID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE email_campaigns SET status = 'failed', updated_at = now() WHERE id = $1 AND status = 'sending'`,
		campaignID,
	)
	if err != nil {
		return false, fmt.Errorf("mailing: failing campaign %d: %w", campaignID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// CampaignStatus reads email_campaigns.status for campaignID.
//
// This exists for #0048's progress snapshot, and the reason is worth stating
// plainly: MarkFailedCampaign above updates email_campaigns ONLY — it never
// touches email_sends. So on physical_address_missing, reply_to_missing, and
// every terminal-class SES error, a campaign reaches 'failed' with its unsent
// rows still 'queued', and publishProgress's Remaining (queued+sending) is
// therefore > 0 on exactly the paths where the send has definitively stopped.
// Terminality is a fact about the CAMPAIGN's status, not an arithmetic
// property of the send counts, and it cannot be derived from them — so the
// snapshot carries it rather than asking the client to infer it (#0048's
// second review, point 3).
func (s *SendStore) CampaignStatus(ctx context.Context, campaignID int64) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM email_campaigns WHERE id = $1`, campaignID,
	).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("mailing: reading status of campaign %d: %w", campaignID, err)
	}
	return status, nil
}

// GatherPreflight reads every fact preflight.go's Preflight function needs
// for campaignID and assembles a PreflightInput — the one place both call
// sites (the worker, authoritative; the campaignPreflightChecker HTTP
// adapter, advisory) read from, so they can never drift.
//
// AudienceCount is evaluated via AudienceStore.Preview, which works
// identically for a 'draft' campaign (the HTTP call site's usual case) and a
// 'scheduled' one (the worker's case) — Preview never requires
// materialization. AudienceCount stays -1 ("not evaluated") when Preview
// itself errors, so empty_audience never fires alongside audience_invalid
// for the same underlying problem (an unknown audience_mode or a missing
// interest set on an any_of/all_of campaign).
//
// RenderErr comes from a dry render through #0042/#0043's renderer with the
// sentinel manage token "PREFLIGHT-DRY-RUN" (per this issue's plan §2) —
// PRD §6.6's pre-send list says "body renders", and this is how that is
// evidenced without sending anything. The rendered bytes are discarded
// either way; the sentinel never reaches a real Message.
func (s *SendStore) GatherPreflight(ctx context.Context, campaignID int64) (PreflightInput, error) {
	var subject, bodyMD string
	var testSentAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT subject, body_md, test_sent_at FROM email_campaigns WHERE id = $1`, campaignID,
	).Scan(&subject, &bodyMD, &testSentAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return PreflightInput{}, ErrCampaignNotFound
	case err != nil:
		return PreflightInput{}, fmt.Errorf("mailing: gathering preflight facts for campaign %d: %w", campaignID, err)
	}

	aud, err := s.audience.LoadAudience(ctx, campaignID)
	if err != nil {
		return PreflightInput{}, err
	}

	physicalAddress, settingsErr := s.settings.GetSetting(ctx, settingPhysicalAddress)

	audienceCount := int64(-1)
	var audienceErr error
	preview, perr := s.audience.Preview(ctx, aud, 0)
	if perr != nil {
		audienceErr = perr
	} else {
		audienceCount = preview.Count
	}

	_, _, renderErr := s.render.Campaign(CampaignRenderInput{
		Subject:         subject,
		BodyMarkdown:    bodyMD,
		BaseURL:         s.baseURL,
		ManageToken:     "PREFLIGHT-DRY-RUN",
		PhysicalAddress: physicalAddress,
	})

	return PreflightInput{
		Subject:         subject,
		BodyMarkdown:    bodyMD,
		AudienceMode:    aud.Mode,
		InterestIDs:     aud.InterestIDs,
		TestSentAt:      testSentAt,
		PhysicalAddress: physicalAddress,
		SettingsErr:     settingsErr,
		ReplyTo:         s.replyTo,
		ListDomain:      s.listDomain,
		BaseURL:         s.baseURL,
		MailerReady:     true, // see MailerReady's own doc comment
		AudienceCount:   audienceCount,
		AudienceErr:     audienceErr,
		RenderErr:       renderErr,
	}, nil
}

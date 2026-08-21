// audience.go answers the one question every campaign-sending call site
// needs and must never answer twice: "which subscribers may receive
// campaign N, and which address do we mail" (#0044; PRD §6.6). It owns the
// four audience predicates (PRD §6.6's all/any_of/all_of/none_selected),
// the dry-run Preview an operator checks before committing, the chunked
// Materialize that turns a campaign's targeting into a fixed set of
// email_sends rows, and the send-time re-check #0045's worker must run
// inside the transaction that claims each batch.
//
// # Materialize is an optimization; it is never the authority
//
// Materialize runs once, on the worker's first claim of a campaign, and
// inserts one email_sends row per eligible subscriber at that moment. That
// snapshot can go stale before a given row is actually sent — a subscriber
// can unsubscribe, hard-bounce, complain, or be manually suppressed in the
// window between materialization and send. PRD §6.5 and CLAUDE.md §9 make
// that a correctness question: a suppressed or unsubscribed address must
// not receive mail even though it is in the snapshot. The authority is
// therefore RecheckEligibleTx, run inside the same transaction that holds
// the FOR UPDATE SKIP LOCKED claim on a batch of queued rows, immediately
// before the SES call (#0045's loop):
//
//  1. BEGIN
//  2. SELECT ... FROM email_sends WHERE campaign_id=$1 AND status='queued'
//     ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED
//  3. RecheckEligibleTx(ctx, tx, campaignID, ids) -> (recipients, skipped)
//  4. MarkSkippedTx(ctx, tx, campaignID, skippedIDs, reason)
//  5. COMMIT, then send only to recipients
//
// Re-materializing periodically during a send is deliberately NOT done: it
// would pick up subscribers who confirmed after the send started, making
// progress totals move backwards on the operator's screen and mailing
// people the confirmed count never covered. A subscriber who becomes
// active after materialization simply is not in that campaign.
//
// # Suppression is checked at both call sites, for different reasons
//
// At materialization, as a set-based anti-join against suppressions — this
// keeps the count an operator confirms honest and keeps rows that would
// only ever be skipped out of the queue. At send (RecheckEligibleTx), this
// is the check that is actually load-bearing for correctness: delete it
// and wrong mail goes out. Both call sites express suppression as SQL
// against the suppressions table in the same statement — reason-blind,
// per #0100/subscribers.SuppressionStore.IsSuppressed's doc comment: any
// suppression row of any reason blocks, regardless of why it was added.
//
// This crosses a package boundary (internal/mailing reading SQL against a
// table internal/subscribers owns) deliberately: the alternative is either
// an N+1 IsSuppressed call per recipient or pulling the whole suppression
// list into Go to filter there, and both are worse than one read-only
// anti-join. This package never writes suppressions.
//
// # RecheckEligibleTx and MarkSkippedTx are transaction-only, by design
//
// A pool-routed version of RecheckEligibleTx would be a TOCTOU with no
// correct use: the whole point is that the eligibility check happens
// inside the same transaction that holds the claim lock, so nothing can
// change a row's eligibility between the check and the send. Providing a
// pool-backed twin would just be a footgun, so none exists.
package mailing

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// ErrAudienceInterestsRequired is returned by Preview/Materialize when
// audience_mode is any_of or all_of and no interest ids are supplied.
//
// This is a deliberate SECOND, independent enforcement of the same rule
// mailing.ErrCampaignInterestsRequired already guards at every campaign
// write (campaigns.go's normalizeAudience) — belt and braces, not shared
// implementation. The reason it must be checked again here, specifically
// for all_of: `(SELECT count(*) ...) = cardinality($n)` evaluates `0 = 0`
// for EVERY subscriber when $n is an empty array, silently turning all_of
// into "the entire list" — a mode that mails everyone when the operator
// selected nothing. That must be impossible at the one place that could
// actually materialize it, not just at the write path that (today) already
// prevents an empty set from being saved.
var ErrAudienceInterestsRequired = errors.New("mailing: audience mode requires at least one interest")

// materializeChunkSize is the default page size Materialize scans and
// inserts per statement. Overridable via AudienceStore.chunkSize in tests
// so multi-chunk paths can be exercised with a handful of seeded rows
// instead of 1000+.
const materializeChunkSize = 1000

// querier is the subset of pgx RecheckEligibleTx/MarkSkippedTx use.
// *pgxpool.Pool and pgx.Tx both satisfy it — the same querier/…Tx idiom
// internal/subscribers/suppressions.go establishes, redeclared unexported
// here per that file's own convention rather than exporting one from
// internal/subscribers. Unlike SuppressionStore.AddTx, there is
// deliberately no pool-backed twin of RecheckEligibleTx — see the package
// doc comment.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Audience is a campaign's targeting: its audience_mode plus the interest
// ids attached to it (campaign_interests), regardless of whether the mode
// actually consumes them — LoadAudience always loads every attached
// interest id, and Preview surfaces a warning when they are attached to a
// mode ('all' or 'none_selected') that ignores them (§4 of this issue's
// plan). Zero-value InterestIDs is valid for every mode.
type Audience struct {
	Mode        string
	InterestIDs []int64
}

// AudiencePreview is Preview's result: a dry-run count and a small sample,
// with any warnings about the request (e.g. interests attached but ignored
// by the campaign's current mode).
type AudiencePreview struct {
	Count    int64
	Sample   []string // at most sampleLimit addresses, subscriber id order
	Warnings []string
}

// MaterializeResult reports what one Materialize call did.
type MaterializeResult struct {
	Inserted int64 // rows this call actually created
	Scanned  int64 // rows matching the predicate, including pre-existing (already materialized) ones
	Chunks   int   // number of chunk statements executed
}

// Recipient is one row eligible to be mailed, as returned by
// RecheckEligibleTx. Email is the snapshot taken at materialization time
// (the address the audit trail records); ManageToken is read FRESH from
// subscribers, because PRD §6.5 rotates it on unsubscribe and a token
// captured at materialization could be stale by the time the message is
// composed — producing a dead one-click unsubscribe link, the single most
// important link in the message.
type Recipient struct {
	SendID       int64
	SubscriberID int64
	Email        string
	ManageToken  string
}

// SkippedSend is one row RecheckEligibleTx found no longer eligible: it was
// in the audience at materialization but is not eligible now, so it must
// never be mailed. Reason is a short, human-readable explanation of which
// invariant failed (status, suppression, or a changed email), for
// MarkSkippedTx's error column and #0049's campaign stats view.
type SkippedSend struct {
	SendID       int64
	SubscriberID int64
	Email        string
	Reason       string
}

// AudienceStore is the data-access layer behind the eligibility question.
// It is internal/mailing's first store with DB code at all — it follows
// internal/subscribers' conventions (narrow querier interface, Tx twins
// only where a caller-owned transaction is actually required) rather than
// inventing new ones.
type AudienceStore struct {
	pool      *pgxpool.Pool
	chunkSize int // 0 means materializeChunkSize; settable in tests only
}

// NewAudienceStore constructs an AudienceStore over the shared connection pool.
func NewAudienceStore(pool *pgxpool.Pool) *AudienceStore {
	return &AudienceStore{pool: pool}
}

// audienceWhere returns the full eligibility+targeting predicate (as a SQL
// fragment referencing the "s" alias for subscribers, without a leading
// WHERE) and its bind arguments, with placeholders numbered starting at
// argOffset+1 so the fragment composes into a statement that already bound
// earlier parameters. Materialize binds $1=campaign_id, $2=cursor,
// $3=chunkSize before this fragment's placeholders begin at $4 (argOffset
// 3); Preview binds nothing before it, so argOffset is 0 and placeholders
// start at $1. This is the one builder every consumer (Preview, Materialize)
// shares, so "share the query, do not write it twice" is a structural
// guarantee, not a convention.
func audienceWhere(aud Audience, argOffset int) (string, []any, error) {
	switch aud.Mode {
	case AudienceAll, AudienceAnyOf, AudienceAllOf, AudienceNoneSelected:
	default:
		return "", nil, ErrUnknownAudienceMode
	}
	if (aud.Mode == AudienceAnyOf || aud.Mode == AudienceAllOf) && len(aud.InterestIDs) == 0 {
		return "", nil, ErrAudienceInterestsRequired
	}

	next := argOffset
	placeholder := func() string {
		next++
		return fmt.Sprintf("$%d", next)
	}

	statusPH := placeholder()
	args := []any{subscribers.StatusActive}

	// Eligibility: active status, written as equality (not NOT IN a
	// list) so a future status added to the vocabulary defaults to NOT
	// receiving mail — the safe direction. Plus a reason-blind anti-join
	// against suppressions.
	where := fmt.Sprintf(
		`s.status = %s AND NOT EXISTS (SELECT 1 FROM suppressions x WHERE x.email = s.email)`,
		statusPH,
	)

	switch aud.Mode {
	case AudienceAll:
		// No additional targeting predicate.
	case AudienceAnyOf:
		idsPH := placeholder()
		args = append(args, aud.InterestIDs)
		where += fmt.Sprintf(
			` AND EXISTS (SELECT 1 FROM subscriber_interests si WHERE si.subscriber_id = s.id AND si.interest_id = ANY(%s))`,
			idsPH,
		)
	case AudienceAllOf:
		idsPH := placeholder()
		args = append(args, aud.InterestIDs)
		where += fmt.Sprintf(
			` AND (SELECT count(*) FROM subscriber_interests si WHERE si.subscriber_id = s.id AND si.interest_id = ANY(%s)) = cardinality(%s)`,
			idsPH, idsPH,
		)
	case AudienceNoneSelected:
		where += ` AND NOT EXISTS (SELECT 1 FROM subscriber_interests si WHERE si.subscriber_id = s.id)`
	}

	return where, args, nil
}

// LoadAudience reads a campaign's audience_mode and every interest id
// attached to it (campaign_interests), regardless of whether that mode
// consumes interests — see Audience's doc comment. Returns
// ErrCampaignNotFound when no email_campaigns row matches campaignID.
func (s *AudienceStore) LoadAudience(ctx context.Context, campaignID int64) (Audience, error) {
	var mode string
	err := s.pool.QueryRow(ctx,
		`SELECT audience_mode FROM email_campaigns WHERE id = $1`, campaignID,
	).Scan(&mode)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Audience{}, ErrCampaignNotFound
	case err != nil:
		return Audience{}, fmt.Errorf("mailing: loading audience mode for campaign %d: %w", campaignID, err)
	}

	ids, err := s.campaignInterestIDs(ctx, campaignID)
	if err != nil {
		return Audience{}, err
	}
	return Audience{Mode: mode, InterestIDs: ids}, nil
}

// campaignInterestIDs returns every interest id attached to campaignID,
// ordered. A local query rather than reuse of CampaignStore.interestIDs
// (unexported on a different type) — both are one-line reads over the same
// table, so duplicating the query is cheaper and clearer than exporting a
// method solely for this one call site.
func (s *AudienceStore) campaignInterestIDs(ctx context.Context, campaignID int64) ([]int64, error) {
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

// audienceWarnings reports non-fatal issues with aud worth surfacing to an
// operator: interests attached to a mode that ignores them.
func audienceWarnings(aud Audience) []string {
	var warnings []string
	if (aud.Mode == AudienceAll || aud.Mode == AudienceNoneSelected) && len(aud.InterestIDs) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"interests are attached to this campaign but ignored in audience mode %q", aud.Mode,
		))
	}
	return warnings
}

// Preview is the dry-run count + sample an operator checks before
// committing to a send, and the count #0041's Send handler compares against
// the operator's typed confirm_count. It reads only — it never inserts an
// email_sends row, which is what makes it safe to poll on every checkbox
// change on the compose screen. sampleLimit bounds the sample slice (the
// HTTP handler uses a server-side constant of 20; a caller wanting count
// only may pass 0).
//
// Preview's count and Materialize's Inserted count must always agree over
// the same Audience and a stable subscriber/suppression snapshot — they
// share audienceWhere so that is a structural guarantee, not a promise.
func (s *AudienceStore) Preview(ctx context.Context, aud Audience, sampleLimit int) (AudiencePreview, error) {
	where, whereArgs, err := audienceWhere(aud, 0)
	if err != nil {
		return AudiencePreview{}, err
	}

	var count int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM subscribers s WHERE `+where, whereArgs...,
	).Scan(&count); err != nil {
		return AudiencePreview{}, fmt.Errorf("mailing: previewing audience: %w", err)
	}

	sample := []string{}
	if sampleLimit > 0 {
		limitPH := fmt.Sprintf("$%d", len(whereArgs)+1)
		sampleArgs := append(append([]any{}, whereArgs...), sampleLimit)
		rows, err := s.pool.Query(ctx,
			`SELECT s.email FROM subscribers s WHERE `+where+` ORDER BY s.id LIMIT `+limitPH, sampleArgs...,
		)
		if err != nil {
			return AudiencePreview{}, fmt.Errorf("mailing: sampling audience: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var email string
			if err := rows.Scan(&email); err != nil {
				return AudiencePreview{}, fmt.Errorf("mailing: scanning audience sample row: %w", err)
			}
			sample = append(sample, email)
		}
		if err := rows.Err(); err != nil {
			return AudiencePreview{}, fmt.Errorf("mailing: iterating audience sample rows: %w", err)
		}
	}

	return AudiencePreview{
		Count:    count,
		Sample:   sample,
		Warnings: audienceWarnings(aud),
	}, nil
}

// materializeChunkQuery is Materialize's per-chunk statement. $1=campaign_id,
// $2=cursor (subscribers.id already scanned), $3=chunk limit; audienceWhere's
// placeholders (bound at argOffset 3) follow starting at $4.
//
// The cursor advances from page (the MATERIALIZED CTE), never from
// RETURNING: RETURNING only yields rows the INSERT actually created, so a
// re-run whose whole chunk conflicts (every row already materialized) would
// return zero rows, leave the cursor unmoved, and loop forever. Reading
// max(id)/count(*) from page instead advances on rows SCANNED, so a
// re-materialize of an already-complete campaign terminates in one chunk
// with Inserted=0 rather than spinning — see the regression test
// TestAudienceStore_Materialize_AllConflictsChunkStillAdvancesCursor.
// MATERIALIZED is explicit so every reference to page in this statement
// sees one identical evaluation, regardless of planner mood.
const materializeChunkQuery = `
WITH page AS MATERIALIZED (
    SELECT s.id, s.email
      FROM subscribers s
     WHERE s.id > $2
       AND %s
     ORDER BY s.id
     LIMIT $3
),
ins AS (
    INSERT INTO email_sends (campaign_id, subscriber_id, email)
    SELECT $1, page.id, page.email FROM page
    ON CONFLICT (campaign_id, subscriber_id) DO NOTHING
    RETURNING subscriber_id
)
SELECT COALESCE(max(page.id), $2), count(*), (SELECT count(*) FROM ins)
  FROM page`

// Materialize turns aud's targeting into a fixed set of email_sends rows
// for campaignID: one row per eligible subscriber, snapshotting only
// (campaign_id, subscriber_id, email) — every other column takes its
// schema default (status='queued', attempts=0, ...). It never updates an
// existing row: the insert is ON CONFLICT (campaign_id, subscriber_id) DO
// NOTHING, never DO UPDATE, because any update clause here would be a
// documented path from 'sent' back to 'queued' — a double-send bug written
// into the schema.
//
// Safely re-runnable: a crash mid-materialization leaves a partial
// audience, and the next call resumes and completes it for free via the
// same ON CONFLICT. Re-running after completion is a no-op that terminates
// in one chunk (MaterializeResult.Inserted == 0) rather than looping — see
// materializeChunkQuery's doc comment for why the cursor logic makes that
// true.
//
// Runs one chunk (default 1000 rows) per autocommit statement — no
// transaction spans the whole call, per the "does not hold a long
// transaction" criterion. ctx is honoured between chunks, so a shutdown
// stops between chunks rather than mid-statement.
func (s *AudienceStore) Materialize(ctx context.Context, campaignID int64, aud Audience) (MaterializeResult, error) {
	where, whereArgs, err := audienceWhere(aud, 3)
	if err != nil {
		return MaterializeResult{}, err
	}

	chunkSize := s.chunkSize
	if chunkSize <= 0 {
		chunkSize = materializeChunkSize
	}

	query := fmt.Sprintf(materializeChunkQuery, where)

	var result MaterializeResult
	var cursor int64
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		args := make([]any, 0, 3+len(whereArgs))
		args = append(args, campaignID, cursor, chunkSize)
		args = append(args, whereArgs...)

		var nextCursor, scanned, inserted int64
		if err := s.pool.QueryRow(ctx, query, args...).Scan(&nextCursor, &scanned, &inserted); err != nil {
			return result, fmt.Errorf("mailing: materializing audience for campaign %d: %w", campaignID, err)
		}
		result.Scanned += scanned
		result.Inserted += inserted
		result.Chunks++
		cursor = nextCursor

		if scanned < int64(chunkSize) {
			break
		}
	}
	return result, nil
}

// recheckReason explains, in a short human-readable phrase, why a
// previously-materialized row is no longer eligible. Computed in Go rather
// than a SQL CASE expression so the three predicates read as plainly here
// as they do in audienceWhere.
func recheckReason(status string, suppressed bool, emailChanged bool) string {
	switch {
	case status != subscribers.StatusActive:
		return fmt.Sprintf("subscriber status is now %q, not active", status)
	case suppressed:
		return "address is now suppressed"
	case emailChanged:
		return "subscriber's email changed since materialization"
	default:
		return "" // unreachable when eligible is false; callers only use this when one of the above is true
	}
}

// RecheckEligibleTx evaluates the eligibility predicate for the given
// email_sends rows (sendIDs, already claimed and locked by the caller —
// typically via FOR UPDATE SKIP LOCKED) inside the caller's own
// transaction q, and splits them into still-eligible Recipients and
// now-ineligible SkippedSends. This is the authority the materialized
// snapshot is not — see the package doc comment's rule: no message leaves
// the system without this check having run, inside the same transaction
// that holds the claim, immediately before the send.
//
// A row is eligible only when its subscriber is currently active, carries
// no suppression row (reason-blind), AND its live subscribers.email still
// equals the email_sends snapshot — the last predicate has no way to fire
// today (there is no email-change flow) but makes explicit the invariant
// "we mail the address we checked" rather than leaving it an unstated
// consequence of the other two.
//
// Only rows still in status='queued' are considered — the same "a row can
// leave queued exactly once" guard MarkSkippedTx and #0045's sent/failed
// transitions all share (§6 of this issue's plan). A sendID that has
// already been marked skipped, sent, or failed comes back in NEITHER
// returned slice on a later call, rather than being re-evaluated and
// re-added to skipped.
//
// Deliberately transaction-only: no pool-backed twin exists, because a
// pool-routed recheck would be a TOCTOU with no correct use (see the
// package doc comment).
func (s *AudienceStore) RecheckEligibleTx(ctx context.Context, q querier, campaignID int64, sendIDs []int64) ([]Recipient, []SkippedSend, error) {
	if len(sendIDs) == 0 {
		return nil, nil, nil
	}

	rows, err := q.Query(ctx,
		`SELECT es.id, es.subscriber_id, es.email, s.status, s.manage_token, s.email,
		        EXISTS (SELECT 1 FROM suppressions x WHERE x.email = s.email) AS suppressed
		   FROM email_sends es
		   JOIN subscribers s ON s.id = es.subscriber_id
		  WHERE es.campaign_id = $1 AND es.id = ANY($2) AND es.status = 'queued'`,
		campaignID, sendIDs,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("mailing: rechecking eligibility for campaign %d: %w", campaignID, err)
	}
	defer rows.Close()

	var recipients []Recipient
	var skipped []SkippedSend
	for rows.Next() {
		var (
			sendID, subscriberID      int64
			snapshotEmail, status     string
			manageToken, currentEmail string
			suppressed                bool
		)
		if err := rows.Scan(&sendID, &subscriberID, &snapshotEmail, &status, &manageToken, &currentEmail, &suppressed); err != nil {
			return nil, nil, fmt.Errorf("mailing: scanning recheck row: %w", err)
		}

		emailChanged := currentEmail != snapshotEmail
		eligible := status == subscribers.StatusActive && !suppressed && !emailChanged
		if eligible {
			recipients = append(recipients, Recipient{
				SendID:       sendID,
				SubscriberID: subscriberID,
				Email:        snapshotEmail,
				ManageToken:  manageToken,
			})
			continue
		}
		skipped = append(skipped, SkippedSend{
			SendID:       sendID,
			SubscriberID: subscriberID,
			Email:        snapshotEmail,
			Reason:       recheckReason(status, suppressed, emailChanged),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("mailing: iterating recheck rows: %w", err)
	}
	return recipients, skipped, nil
}

// MarkSkippedTx transitions the given email_sends rows to status='skipped'
// with error=reason, inside the caller's own transaction q. Guarded by
// AND status = 'queued' so a row can leave 'queued' exactly once — a
// restart that replays a batch finds zero rows still queued and updates
// nothing, matching the same idempotency guard #0045's own sent/failed
// transitions must use. Returns the number of rows actually updated.
func (s *AudienceStore) MarkSkippedTx(ctx context.Context, q querier, campaignID int64, sendIDs []int64, reason string) (int64, error) {
	if len(sendIDs) == 0 {
		return 0, nil
	}
	tag, err := q.Exec(ctx,
		`UPDATE email_sends
		    SET status = 'skipped', error = $3
		  WHERE campaign_id = $1 AND id = ANY($2) AND status = 'queued'`,
		campaignID, sendIDs, reason,
	)
	if err != nil {
		return 0, fmt.Errorf("mailing: marking sends skipped for campaign %d: %w", campaignID, err)
	}
	return tag.RowsAffected(), nil
}

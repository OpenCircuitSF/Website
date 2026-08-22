// campaign_stats.go: the read-only reconciliation layer backing #0049's
// per-campaign stats screen (GET /admin/campaigns/{id}/stats, PRD §11).
//
// # Why bounced/complained are reconciled from email_events, not read off email_sends.status
//
// Nothing in this package's send path, nor internal/sesnotify's SES/SNS
// event ingestion (#0038), ever writes email_sends.status to 'bounced' or
// 'complained' — worker_store.go's MarkSent only ever writes 'sent', and
// every bounce/complaint notification lands exclusively in email_events
// (#0038's own scope; that handler never touches email_sends). #0131
// confirmed this and removed both values from migration 000017's
// email_sends_status_check (widened by 000018 to add 'sending', which does
// not touch this decision) — the CHECK now enforces at the database level
// what was already true in practice, so a raw
// `COUNT(*) FILTER (WHERE status = 'bounced')` is not merely always zero,
// it is now structurally guaranteed to be. The true count comes from
// joining email_sends to email_events on ses_message_id —
// idx_email_sends_message_id (000017) and idx_email_events_message_id
// (000014) exist specifically for this join.
//
// StatusCounts below still reports Bounced/Complained fields, always zero
// by construction — kept rather than removed so the response shape stays
// stable and the guard test proving they read zero
// (internal/handlers/admin_campaign_stats_test.go's
// TestAdminCampaignStats_ReconcilesBounceAndComplaintFromEvents) keeps
// guarding something real. EventCounts is the reconciled source
// web/src/lib/campaignStats.ts's buildStatBuckets substitutes into the
// bounced/complained buckets for display — see that function's doc comment
// for why the client, not this file, owns that substitution decision.
package mailing

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CampaignStatsStore reads per-campaign send outcome stats over email_sends
// and email_events. Deliberately its own store — not folded into SendStore
// (the send worker's own data layer, which also carries audience/settings/
// render dependencies this read never needs) or CampaignStore (the CRUD
// write path). A stats read is meaningful even on an instance where
// MAILER_NOOP=true makes cmd/opencircuit/main.go's sendStore nil
// (newSendStoreIfEnabled) — whether THIS instance can send has no bearing
// on whether a past campaign's outcome can be read.
type CampaignStatsStore struct {
	pool *pgxpool.Pool
}

// NewCampaignStatsStore constructs a CampaignStatsStore over the shared pool.
func NewCampaignStatsStore(pool *pgxpool.Pool) *CampaignStatsStore {
	return &CampaignStatsStore{pool: pool}
}

// CampaignSendCounts is the raw email_sends.status breakdown for one
// campaign. Bounced and Complained are kept as fields even though migration
// 000017 (as amended by #0131, widened by 000018) no longer admits either
// value into the column — see this file's package doc comment for why they
// stay, always reading zero. Skipped is its own field, distinct from Failed
// (carried in from #0044's plan: a recipient who unsubscribed or was
// suppressed between materialization and send is not a delivery failure —
// counting it as one would misreport a correctly-working unsubscribe path).
type CampaignSendCounts struct {
	Queued     int64
	Sending    int64
	Sent       int64
	Failed     int64
	Bounced    int64
	Complained int64
	Skipped    int64
}

// StatusCounts returns the raw per-status row counts for campaignID. See
// this file's package doc comment for why Bounced/Complained read zero by
// construction — EventCounts below is the reconciled source for those two.
func (s *CampaignStatsStore) StatusCounts(ctx context.Context, campaignID int64) (CampaignSendCounts, error) {
	var c CampaignSendCounts
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'queued'),
		        count(*) FILTER (WHERE status = 'sending'),
		        count(*) FILTER (WHERE status = 'sent'),
		        count(*) FILTER (WHERE status = 'failed'),
		        count(*) FILTER (WHERE status = 'bounced'),
		        count(*) FILTER (WHERE status = 'complained'),
		        count(*) FILTER (WHERE status = 'skipped')
		   FROM email_sends WHERE campaign_id = $1`, campaignID,
	).Scan(&c.Queued, &c.Sending, &c.Sent, &c.Failed, &c.Bounced, &c.Complained, &c.Skipped)
	if err != nil {
		return CampaignSendCounts{}, fmt.Errorf("mailing: counting send statuses for campaign %d: %w", campaignID, err)
	}
	return c, nil
}

// CampaignEventCounts is the reconciled bounce/complaint count for one
// campaign, joined from email_events via ses_message_id — see this file's
// package doc comment for why this, not CampaignSendCounts.Bounced/
// Complained, is the number the stats screen actually surfaces.
type CampaignEventCounts struct {
	Bounced    int64
	Complained int64
}

// EventCounts reconciles campaignID's email_sends against email_events: how
// many distinct sent messages have at least one Bounce/Complaint event
// linked by ses_message_id. `count(DISTINCT s.id)`, not a plain row count,
// so a recipient who generated more than one event of the same type (e.g. a
// bounce reported twice by the receiving MTA) is counted once, not once per
// event — this is a count of AFFECTED RECIPIENTS, which is what the 0.3%
// complaint-rate threshold is defined against, not a count of raw
// notifications. The join naturally excludes email_sends rows with a NULL
// ses_message_id (never attempted, or claimed but not yet sent) since
// Postgres NULL never equals NULL.
func (s *CampaignStatsStore) EventCounts(ctx context.Context, campaignID int64) (CampaignEventCounts, error) {
	var c CampaignEventCounts
	err := s.pool.QueryRow(ctx,
		`SELECT count(DISTINCT s.id) FILTER (WHERE e.event_type = 'Bounce'),
		        count(DISTINCT s.id) FILTER (WHERE e.event_type = 'Complaint')
		   FROM email_sends s
		   JOIN email_events e ON e.ses_message_id = s.ses_message_id
		  WHERE s.campaign_id = $1`, campaignID,
	).Scan(&c.Bounced, &c.Complained)
	if err != nil {
		return CampaignEventCounts{}, fmt.Errorf("mailing: reconciling bounce/complaint events for campaign %d: %w", campaignID, err)
	}
	return c, nil
}

// FailedSend is one email_sends row at status='failed', for the "failed
// sends listable with their error messages" acceptance criterion.
type FailedSend struct {
	ID       int64
	Email    string
	Error    string
	Attempts int
}

// FailedSends returns every failed send row for campaignID, ordered by id
// so the list order is stable across requests.
func (s *CampaignStatsStore) FailedSends(ctx context.Context, campaignID int64) ([]FailedSend, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, email, coalesce(error, ''), attempts
		   FROM email_sends
		  WHERE campaign_id = $1 AND status = 'failed'
		  ORDER BY id`, campaignID,
	)
	if err != nil {
		return nil, fmt.Errorf("mailing: listing failed sends for campaign %d: %w", campaignID, err)
	}
	defer rows.Close()
	var out []FailedSend
	for rows.Next() {
		var f FailedSend
		if err := rows.Scan(&f.ID, &f.Email, &f.Error, &f.Attempts); err != nil {
			return nil, fmt.Errorf("mailing: scanning failed send row for campaign %d: %w", campaignID, err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mailing: iterating failed send rows for campaign %d: %w", campaignID, err)
	}
	return out, nil
}
